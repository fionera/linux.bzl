package kconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	linuxProbeTimeout     = 20 * time.Second
	linuxProbeOutputLimit = 64 << 10
)

// LinuxToolProbeOptions describes integrity-pinned host tools used to answer
// Kconfig and Kbuild capability probes. Tools are executed directly with argv;
// probe text is never passed to a command shell.
type LinuxToolProbeOptions struct {
	Profile      string
	Architecture string
	TargetTriple string
	ClangPath    string
	LLDPath      string
	TempDir      string
	Identity     string
	Timeout      time.Duration
	OutputLimit  int
}

// LinuxToolProbe executes and memoizes safe compiler/linker capability probes.
type LinuxToolProbe struct {
	profile      LinuxTargetProfile
	clangPath    string
	lldPath      string
	tempDir      string
	identity     string
	timeout      time.Duration
	outputLimit  int
	mu           sync.Mutex
	cache        map[string]bool
	clangVersion string
	clangCode    int
	lldCode      int
}

func NewLinuxToolProbe(opts LinuxToolProbeOptions) (*LinuxToolProbe, error) {
	profile, err := LinuxTargetProfileByName(opts.Profile)
	if err != nil {
		return nil, err
	}
	if err := profile.ValidateTargetIdentity(opts.Architecture, opts.TargetTriple); err != nil {
		return nil, err
	}
	if opts.ClangPath == "" || opts.LLDPath == "" {
		return nil, fmt.Errorf("real Linux probes require both clang and ld.lld paths")
	}
	clangPath, err := filepath.Abs(opts.ClangPath)
	if err != nil {
		return nil, fmt.Errorf("resolve clang path: %w", err)
	}
	lldPath, err := filepath.Abs(opts.LLDPath)
	if err != nil {
		return nil, fmt.Errorf("resolve ld.lld path: %w", err)
	}
	for name, path := range map[string]string{"clang": clangPath, "ld.lld": lldPath} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, fmt.Errorf("stat probe %s %q: %w", name, path, statErr)
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("probe %s %q is not an executable file", name, path)
		}
	}
	identity := opts.Identity
	if identity == "" {
		identity, err = toolPairIdentity(clangPath, lldPath)
		if err != nil {
			return nil, err
		}
	}
	p := &LinuxToolProbe{
		profile: profile, clangPath: clangPath, lldPath: lldPath,
		tempDir: opts.TempDir, identity: identity, cache: map[string]bool{},
		timeout: opts.Timeout, outputLimit: opts.OutputLimit,
	}
	if p.timeout <= 0 {
		p.timeout = linuxProbeTimeout
	}
	if p.outputLimit <= 0 {
		p.outputLimit = linuxProbeOutputLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	clangText, err := p.run(ctx, clangPath, []string{"--version"}, nil)
	if err != nil {
		return nil, fmt.Errorf("identify clang probe tool: %w", err)
	}
	p.clangVersion, p.clangCode, err = parseLLVMVersion(clangText, "clang")
	if err != nil {
		return nil, err
	}
	if p.clangCode != linuxProbeCCVersion {
		return nil, fmt.Errorf("probe clang version is %d, want pinned LLVM %d", p.clangCode, linuxProbeCCVersion)
	}
	lldText, err := p.run(ctx, lldPath, []string{"--version"}, nil)
	if err != nil {
		return nil, fmt.Errorf("identify ld.lld probe tool: %w", err)
	}
	_, p.lldCode, err = parseLLVMVersion(lldText, "LLD")
	if err != nil {
		return nil, err
	}
	if p.lldCode != linuxProbeLDVersion {
		return nil, fmt.Errorf("probe ld.lld version is %d, want pinned LLVM %d", p.lldCode, linuxProbeLDVersion)
	}
	return p, nil
}

func (p *LinuxToolProbe) Identity() string { return p.identity }

func toolPairIdentity(paths ...string) (string, error) {
	h := sha256.New()
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("hash probe tool %q: %w", path, err)
		}
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash probe tool %q: %w", path, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close probe tool %q: %w", path, closeErr)
		}
		h.Write([]byte{0})
	}
	return "sha256-" + hex.EncodeToString(h.Sum(nil)), nil
}

var llvmVersionPattern = regexp.MustCompile(`(?i)(?:clang version|LLD(?: version)?) ([0-9]+)\.([0-9]+)\.([0-9]+)`)

func parseLLVMVersion(output, tool string) (string, int, error) {
	line := strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	match := llvmVersionPattern.FindStringSubmatch(line)
	if match == nil {
		return "", 0, fmt.Errorf("probe %s returned an unsupported version line %q", tool, line)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	if minor > 99 || patch > 99 {
		return "", 0, fmt.Errorf("probe %s version cannot be represented by Linux: %q", tool, line)
	}
	return line, major*10000 + minor*100 + patch, nil
}

// SupportsOption performs one controlled cc-option/as-option/ld-option probe.
// A normal non-zero tool exit reports unsupported; malformed argv and tool
// execution failures are errors.
func (p *LinuxToolProbe) SupportsOption(ctx context.Context, kind string, candidate, probeContext []string) (bool, error) {
	if kind != "cc_option" && kind != "as_option" && kind != "ld_option" {
		return false, fmt.Errorf("unsupported Linux tool probe kind %q", kind)
	}
	if err := validateProbeCandidate(kind, candidate); err != nil {
		return false, fmt.Errorf("invalid %s candidate: %w", kind, err)
	}
	execContext, err := sanitizeProbeContext(kind, probeContext)
	if err != nil {
		return false, fmt.Errorf("invalid %s context: %w", kind, err)
	}
	key := strings.Join([]string{
		p.identity, p.profile.Name, p.profile.TargetTriple, kind,
		strings.Join(probeContext, "\x00"), strings.Join(candidate, "\x00"),
	}, "\x01")
	p.mu.Lock()
	value, ok := p.cache[key]
	p.mu.Unlock()
	if ok {
		return value, nil
	}

	timedCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var supported bool
	if kind == "ld_option" {
		args := append([]string{"-v"}, execContext...)
		args = append(args, candidate...)
		_, err = p.run(timedCtx, p.lldPath, args, nil)
	} else {
		output, createErr := os.CreateTemp(p.tempDir, "linux-bzl-probe-*.o")
		if createErr != nil {
			return false, fmt.Errorf("create Linux probe output: %w", createErr)
		}
		outputPath := output.Name()
		if closeErr := output.Close(); closeErr != nil {
			os.Remove(outputPath)
			return false, fmt.Errorf("close Linux probe output: %w", closeErr)
		}
		defer os.Remove(outputPath)
		language := "c"
		if kind == "as_option" {
			language = "assembler-with-cpp"
		}
		args := []string{"--target=" + p.profile.TargetTriple, "-Werror"}
		args = append(args, execContext...)
		args = append(args, candidate...)
		args = append(args, "-x", language, "-c", "-o", outputPath, "-")
		_, err = p.run(timedCtx, p.clangPath, args, []byte("\n"))
	}
	if err == nil {
		supported = true
	} else if _, ok := err.(*exec.ExitError); !ok {
		return false, err
	}
	p.mu.Lock()
	p.cache[key] = supported
	p.mu.Unlock()
	return supported, nil
}

// SupportsSource compiles one allowlisted Kconfig feature-test source using
// controlled language/input/output arguments.
func (p *LinuxToolProbe) SupportsSource(ctx context.Context, language string, candidate []string, source string) (bool, error) {
	if language != "c" && language != "assembler-with-cpp" {
		return false, fmt.Errorf("unsupported Linux source probe language %q", language)
	}
	if len(candidate) != 0 {
		if err := validateProbeCandidate("source", candidate); err != nil {
			return false, fmt.Errorf("invalid Linux source probe candidate: %w", err)
		}
	}
	digest := sha256.Sum256([]byte(source))
	key := strings.Join([]string{
		p.identity, p.profile.Name, p.profile.TargetTriple, "source", language,
		strings.Join(candidate, "\x00"), hex.EncodeToString(digest[:]),
	}, "\x01")
	p.mu.Lock()
	value, ok := p.cache[key]
	p.mu.Unlock()
	if ok {
		return value, nil
	}
	output, err := os.CreateTemp(p.tempDir, "linux-bzl-source-probe-*.o")
	if err != nil {
		return false, fmt.Errorf("create Linux source probe output: %w", err)
	}
	outputPath := output.Name()
	if err := output.Close(); err != nil {
		os.Remove(outputPath)
		return false, fmt.Errorf("close Linux source probe output: %w", err)
	}
	defer os.Remove(outputPath)
	timedCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	args := []string{"--target=" + p.profile.TargetTriple, "-Werror"}
	args = append(args, candidate...)
	args = append(args, "-x", language, "-c", "-o", outputPath, "-")
	_, runErr := p.run(timedCtx, p.clangPath, args, []byte(source))
	supported := runErr == nil
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return false, runErr
		}
	}
	p.mu.Lock()
	p.cache[key] = supported
	p.mu.Unlock()
	return supported, nil
}

func validateProbeCandidate(kind string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty candidate")
	}
	operand := false
	operandOption := ""
	for _, arg := range argv {
		if err := validateProbeToken(arg); err != nil {
			return err
		}
		if operand {
			operand = false
			if !safeProbeOperand(operandOption, arg) {
				return fmt.Errorf("unsafe operand %q for %s", arg, operandOption)
			}
			continue
		}
		if forbiddenProbeOption(arg) {
			return fmt.Errorf("file/plugin/output option is prohibited: %q", arg)
		}
		switch arg {
		case "-o", "--output", "-x", "-c", "-S", "-E":
			return fmt.Errorf("probe-controlled compiler mode argument is prohibited: %q", arg)
		case "--param", "-mllvm", "-m":
			operand = true
			operandOption = arg
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return fmt.Errorf("input path or positional argument is prohibited: %q", arg)
		}
	}
	if operand {
		return fmt.Errorf("missing operand for final option")
	}
	return nil
}

func validateProbeToken(arg string) error {
	if arg == "" || strings.ContainsAny(arg, "\x00\r\n") {
		return fmt.Errorf("empty or control-character argument %q", arg)
	}
	if strings.HasPrefix(arg, "@") {
		return fmt.Errorf("response files are prohibited: %q", arg)
	}
	return nil
}

func forbiddenProbeOption(arg string) bool {
	lower := strings.ToLower(arg)
	for _, prefix := range []string{
		"-fplugin", "-fpass-plugin", "-load", "--plugin", "-plugin",
		"-xclang", "-save-temps", "--save-temps", "-ftime-trace",
		"-xlinker", "-xassembler", "-wl,",
		"-serialize-diagnostics", "-mj", "-fprofile", "-fcoverage",
		"--script", "-t", "--version-script", "-map", "--map",
		"--dependency-file", "--sysroot", "-l", "--library-path",
		"-i", "-include", "-isystem", "-iquote", "-idirafter",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(lower, "-wa,") {
		forwarded := strings.TrimPrefix(lower, "-wa,")
		return strings.Contains(forwarded, "-i") || strings.Contains(forwarded, "-a=") || strings.Contains(forwarded, "--listing")
	}
	return false
}

func safeProbeOperand(option, operand string) bool {
	if strings.ContainsAny(operand, `/\\`) || strings.HasPrefix(operand, "@") {
		return false
	}
	switch option {
	case "-m":
		return regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`).MatchString(operand)
	case "--param":
		return regexp.MustCompile(`^[A-Za-z0-9_-]+=[A-Za-z0-9_-]+$`).MatchString(operand)
	case "-mllvm":
		switch operand {
		case "-asan-kernel-mem-intrinsic-prefix=1", "-msan-disable-checks=1", "-tsan-compound-read-before-write=1", "-tsan-distinguish-volatile=1":
			return true
		}
	}
	return false
}

// KBUILD_{CPP,C,A,LD}FLAGS contain include/search paths that are irrelevant to
// option acceptance. They are deliberately omitted rather than handed to a
// repository-time probe. Output/plugin options remain hard errors.
func sanitizeProbeContext(kind string, argv []string) ([]string, error) {
	out := make([]string, 0, len(argv))
	skipOperand := false
	for _, arg := range argv {
		if err := validateProbeToken(arg); err != nil {
			return nil, err
		}
		if skipOperand {
			skipOperand = false
			continue
		}
		lower := strings.ToLower(arg)
		if arg == "-I" || arg == "-isystem" || arg == "-include" || arg == "-iquote" || arg == "-idirafter" || arg == "-L" {
			skipOperand = true
			continue
		}
		if strings.HasPrefix(lower, "-i") || strings.HasPrefix(lower, "-l") || strings.HasPrefix(lower, "--sysroot=") {
			continue
		}
		if forbiddenProbeOption(arg) {
			return nil, fmt.Errorf("file/plugin/output option is prohibited: %q", arg)
		}
		if !strings.HasPrefix(arg, "-") {
			return nil, fmt.Errorf("positional context argument is prohibited: %q", arg)
		}
		out = append(out, arg)
	}
	if skipOperand {
		return nil, fmt.Errorf("missing path operand in probe context")
	}
	return out, nil
}

func (p *LinuxToolProbe) run(ctx context.Context, executable string, args []string, input []byte) (string, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "PATH="}
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	output := &limitedProbeBuffer{remaining: p.outputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctx.Err() != nil {
		return output.String(), fmt.Errorf("Linux probe timed out or was cancelled: %w", ctx.Err())
	}
	if output.exceeded {
		return output.String(), fmt.Errorf("Linux probe output exceeded %d bytes", p.outputLimit)
	}
	return output.String(), err
}

type limitedProbeBuffer struct {
	buffer    bytes.Buffer
	remaining int
	exceeded  bool
}

func (w *limitedProbeBuffer) Write(data []byte) (int, error) {
	original := len(data)
	if len(data) > w.remaining {
		data = data[:w.remaining]
		w.exceeded = true
	}
	_, _ = w.buffer.Write(data)
	w.remaining -= len(data)
	return original, nil
}

func (w *limitedProbeBuffer) String() string { return w.buffer.String() }
