package kconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeProbeTool(t *testing.T, path, version, counter string) {
	t.Helper()
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo '` + version + `'
  exit 0
fi
printf x >> '` + counter + `'
for arg in "$@"; do
  if [ "$arg" = "-fnot-supported" ]; then
    exit 1
  fi
done
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func testRealToolProbe(t *testing.T, profile string) (*LinuxToolProbe, string) {
	t.Helper()
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	clang := filepath.Join(dir, "clang")
	lld := filepath.Join(dir, "ld.lld")
	writeProbeTool(t, clang, "clang version 22.1.8", counter)
	writeProbeTool(t, lld, "LLD version 22.1.8", counter)
	target, err := LinuxTargetProfileByName(profile)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewLinuxToolProbe(LinuxToolProbeOptions{
		Profile: profile, Architecture: target.Arch, TargetTriple: target.TargetTriple,
		ClangPath: clang, LLDPath: lld, TempDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return probe, counter
}

func TestLinuxToolProbeRunsAndCachesRealCompilerProbe(t *testing.T) {
	probe, counter := testRealToolProbe(t, "armv7")
	for i := 0; i < 2; i++ {
		supported, err := probe.SupportsOption(context.Background(), "cc_option", []string{"-fno-stack-protector"}, nil)
		if err != nil || !supported {
			t.Fatalf("SupportsOption() = %v, %v", supported, err)
		}
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x" {
		t.Fatalf("tool executed %d times, want one cached execution", len(data))
	}
	if !strings.HasPrefix(probe.Identity(), "sha256-") {
		t.Fatalf("identity = %q", probe.Identity())
	}
}

func TestLinuxToolProbeReturnsUnsupportedExit(t *testing.T) {
	probe, _ := testRealToolProbe(t, "ppc64le")
	supported, err := probe.SupportsOption(context.Background(), "cc_option", []string{"-fnot-supported"}, nil)
	if err != nil || supported {
		t.Fatalf("SupportsOption() = %v, %v; want false, nil", supported, err)
	}
}

func TestLinuxToolProbeRejectsUnpinnedVersion(t *testing.T) {
	dir := t.TempDir()
	clang := filepath.Join(dir, "clang")
	lld := filepath.Join(dir, "ld.lld")
	writeProbeTool(t, clang, "clang version 21.1.8", filepath.Join(dir, "count"))
	writeProbeTool(t, lld, "LLD 22.1.8", filepath.Join(dir, "count"))
	_, err := NewLinuxToolProbe(LinuxToolProbeOptions{
		Profile: "x86_64", Architecture: "x86", TargetTriple: "x86_64-linux-gnu",
		ClangPath: clang, LLDPath: lld,
	})
	if err == nil || !strings.Contains(err.Error(), "want pinned LLVM") {
		t.Fatalf("NewLinuxToolProbe() error = %v", err)
	}
}

func TestLinuxToolProbeRunsAssemblerProbe(t *testing.T) {
	probe, counter := testRealToolProbe(t, "aarch64")
	supported, err := probe.SupportsOption(context.Background(), "as_option", []string{"-Wa,--fatal-warnings"}, []string{"-D__ASSEMBLY__"})
	if err != nil || !supported {
		t.Fatalf("SupportsOption(as_option) = %v, %v", supported, err)
	}
	if data, readErr := os.ReadFile(counter); readErr != nil || string(data) != "x" {
		t.Fatalf("assembler probe counter = %q, %v", data, readErr)
	}
}

func TestLinuxProbeShellWithToolsCompilesAllowlistedSource(t *testing.T) {
	probe, counter := testRealToolProbe(t, "aarch64")
	shell, err := LinuxProbeShellWithTools(probe, LinuxProbeDefaultRustcVersion, LinuxProbeDefaultRustcLLVMVersion)
	if err != nil {
		t.Fatal(err)
	}
	command := `{ printf "%b\n" ".arch_extension lse" | clang -fintegrated-as -Wa,--fatal-warnings -c -x assembler-with-cpp -o /dev/null -; } >/dev/null 2>&1 && echo "y" || echo "n"`
	got, err := shell(context.Background(), command)
	if err != nil || got != "y" {
		t.Fatalf("shell() = %q, %v", got, err)
	}
	if data, readErr := os.ReadFile(counter); readErr != nil || string(data) != "x" {
		t.Fatalf("source probe counter = %q, %v", data, readErr)
	}
}

func TestLinuxToolProbeFailsClosedBeforeExecution(t *testing.T) {
	probe, _ := testRealToolProbe(t, "riscv64")
	for _, candidate := range [][]string{
		{"@attacker.rsp"},
		{"/tmp/input.c"},
		{"-o", "/tmp/owned"},
		{"-DOK=1\n-fplugin=bad"},
		{"-fplugin=/tmp/evil.so"},
		{"-mllvm", "-load=/tmp/evil.so"},
		{"--script=/tmp/evil.ld"},
		{"--plugin=/tmp/evil.so"},
		{"-Map=/tmp/owned"},
		{"-L/tmp/evil"},
	} {
		if _, err := probe.SupportsOption(context.Background(), "cc_option", candidate, nil); err == nil {
			t.Fatalf("SupportsOption(%q) unexpectedly succeeded", candidate)
		}
	}
}

func TestLinuxToolProbeTimeoutAndOutputCap(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "timeout", body: `/bin/sleep 2`, want: "timed out"},
		{name: "output", body: `i=0; while [ "$i" -lt 200 ]; do printf x; i=$((i + 1)); done`, want: "output exceeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			clang := filepath.Join(dir, "clang")
			lld := filepath.Join(dir, "ld.lld")
			tool := `#!/bin/sh
if [ "$1" = "--version" ]; then echo 'clang version 22.1.8'; exit 0; fi
` + test.body + `
`
			if err := os.WriteFile(clang, []byte(tool), 0o755); err != nil {
				t.Fatal(err)
			}
			lldTool := strings.Replace(tool, "clang version", "LLD version", 1)
			if err := os.WriteFile(lld, []byte(lldTool), 0o755); err != nil {
				t.Fatal(err)
			}
			probe, err := NewLinuxToolProbe(LinuxToolProbeOptions{
				Profile: "x86_64", Architecture: "x86", TargetTriple: "x86_64-linux-gnu",
				ClangPath: clang, LLDPath: lld, TempDir: dir, Timeout: 50 * time.Millisecond, OutputLimit: 32,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = probe.SupportsOption(context.Background(), "cc_option", []string{"-fno-stack-protector"}, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("SupportsOption() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLinuxProbeShellWithToolsUsesMeasuredVersions(t *testing.T) {
	probe, _ := testRealToolProbe(t, "x86_64")
	shell, err := LinuxProbeShellWithTools(probe, LinuxProbeDefaultRustcVersion, LinuxProbeDefaultRustcLLVMVersion)
	if err != nil {
		t.Fatal(err)
	}
	for command, want := range map[string]string{
		"/src/scripts/cc-version.sh clang":  "Clang 220108",
		"/src/scripts/ld-version.sh ld.lld": "LLD 220108",
		"clang --version":                   "clang version 22.1.8",
	} {
		got, runErr := shell(context.Background(), command)
		if runErr != nil || got != want {
			t.Fatalf("shell(%q) = %q, %v; want %q", command, got, runErr, want)
		}
	}
}

func TestParseLinuxSourceProbeAcceptsKconfigCompilerVariables(t *testing.T) {
	for _, compiler := range []string{"$CC", "$(CC)", "/pinned/bin/clang"} {
		command := "echo 'int foo(void) { return 0; }' | " + compiler + " $(CLANG_FLAGS) -x c - -c -o /dev/null -Werror"
		source, candidate, err := parseLinuxSourceProbe(command)
		if err != nil {
			t.Fatalf("parseLinuxSourceProbe(%q): %v", command, err)
		}
		if source != "int foo(void) { return 0; }" {
			t.Fatalf("source = %q", source)
		}
		if got, want := strings.Join(candidate, " "), "-fintegrated-as -Werror"; got != want {
			t.Fatalf("candidate = %q, want %q", got, want)
		}
	}
}

func TestKnownRELRProbeAcceptsPinnedToolPaths(t *testing.T) {
	command := `env "CC=/pinned/bin/clang" "LD=/pinned/bin/ld.lld" "NM=llvm-nm" "OBJCOPY=llvm-objcopy" /src/scripts/tools-support-relr.sh`
	if !isKnownRELRProbe(command) {
		t.Fatalf("isKnownRELRProbe(%q) = false", command)
	}
}
