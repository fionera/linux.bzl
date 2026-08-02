package kconfig

import (
	"context"
	"strings"
	"testing"
)

func TestLinuxTargetProfiles(t *testing.T) {
	want := map[string][2]string{
		"x86_64":  {"x86", "x86_64-linux-gnu"},
		"aarch64": {"arm64", "aarch64-linux-gnu"},
		"armv7":   {"arm", "arm-linux-gnueabi"},
		"riscv64": {"riscv", "riscv64-linux-gnu"},
		"ppc64le": {"powerpc", "powerpc64le-linux-gnu"},
	}
	for name, identity := range want {
		profile, err := LinuxTargetProfileByName(name)
		if err != nil {
			t.Fatalf("LinuxTargetProfileByName(%q): %v", name, err)
		}
		if profile.Arch != identity[0] || profile.TargetTriple != identity[1] {
			t.Fatalf("profile %q = %#v", name, profile)
		}
	}
}

func TestArchitectureRootSymbolIsDerivedByKconfig(t *testing.T) {
	profile, _ := LinuxTargetProfileByName("armv7")
	prepared, err := profile.PrepareTargetConfig(map[string]string{"CONFIG_SMP": "y"})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := Parse(context.Background(), strings.NewReader(`
config ARM
	def_bool y
config SMP
	bool "SMP"
`), "arch/arm/Kconfig", Options{})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := tree.ResolveConfig("armv7", prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateResolvedArchitecture(resolved); err != nil {
		t.Fatal(err)
	}
	if got := resolved.Value("CONFIG_ARM"); got != "y" {
		t.Fatalf("CONFIG_ARM = %q, want Kconfig-derived y", got)
	}
}

func TestPrepareTargetConfigDoesNotSeedRootArchitectureSymbol(t *testing.T) {
	profile, _ := LinuxTargetProfileByName("armv7")
	prepared, err := profile.PrepareTargetConfig(map[string]string{"CONFIG_SMP": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prepared["CONFIG_ARM"]; ok {
		t.Fatalf("platform preparation seeded CONFIG_ARM: %#v", prepared)
	}
}

func TestPrepareTargetConfigRejectsContradiction(t *testing.T) {
	profile, _ := LinuxTargetProfileByName("ppc64le")
	for _, raw := range []map[string]string{
		{"CONFIG_ARM": "y"},
		{"CONFIG_PPC64": "n"},
		{"CONFIG_CPU_BIG_ENDIAN": "y"},
	} {
		if _, err := profile.PrepareTargetConfig(raw); err == nil {
			t.Fatalf("PrepareTargetConfig(%#v) unexpectedly succeeded", raw)
		}
	}
}

func TestPrepareTargetConfigRejectsUnsupportedX86AndForeignPPC64Selectors(t *testing.T) {
	profile, _ := LinuxTargetProfileByName("x86_64")
	for _, symbol := range []string{"CONFIG_X86_32", "CONFIG_PPC64"} {
		if _, err := profile.PrepareTargetConfig(map[string]string{symbol: "y"}); err == nil {
			t.Errorf("PrepareTargetConfig(%s=y) unexpectedly succeeded for x86_64", symbol)
		}
	}

	ppc, _ := LinuxTargetProfileByName("ppc64le")
	if _, err := ppc.PrepareTargetConfig(map[string]string{"CONFIG_PPC64": "y"}); err != nil {
		t.Fatalf("PrepareTargetConfig(CONFIG_PPC64=y) rejected ppc64le: %v", err)
	}
}

func TestTargetIdentityIsAtomic(t *testing.T) {
	profile, _ := LinuxTargetProfileByName("riscv64")
	if err := profile.ValidateTargetIdentity("riscv", "riscv64-linux-gnu"); err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateTargetIdentity("arm", "riscv64-linux-gnu"); err == nil || !strings.Contains(err.Error(), "ARCH") {
		t.Fatalf("wrong ARCH error = %v", err)
	}
	if err := profile.ValidateTargetIdentity("riscv", "riscv32-linux-gnu"); err == nil || !strings.Contains(err.Error(), "target triple") {
		t.Fatalf("wrong triple error = %v", err)
	}
}
