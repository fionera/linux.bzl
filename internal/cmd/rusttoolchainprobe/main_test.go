package main

import (
	"strings"
	"testing"

	"github.com/hermeticbuild/linux.bzl/internal/rusttoolchain"
)

func mustParseProbe(t *testing.T, release, commitDate, llvmVersion string) rusttoolchain.Probe {
	t.Helper()
	probe, err := rusttoolchain.ParseVerbose(strings.Join([]string{
		"rustc " + release,
		"release: " + release,
		"commit-date: " + commitDate,
		"LLVM version: " + llvmVersion,
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return probe
}

func TestValidateCompilerIdentity(t *testing.T) {
	probe := mustParseProbe(t, "1.98.0-nightly", "2026-06-24", "22.1.7")
	if err := validateCompilerIdentity(probe, probe); err != nil {
		t.Fatalf("validateCompilerIdentity() rejected identical probes: %v", err)
	}
}

func TestValidateCompilerIdentityRejectsMismatch(t *testing.T) {
	target := mustParseProbe(t, "1.98.0-nightly", "2026-06-24", "22.1.7")
	host := mustParseProbe(t, "1.97.0", "2026-03-01", "22.1.6")
	err := validateCompilerIdentity(target, host)
	if err == nil {
		t.Fatal("validateCompilerIdentity() accepted different compiler probes")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("validateCompilerIdentity() error = %q", err)
	}
}

func TestValidateCompilerIdentityRejectsDifferentVersionText(t *testing.T) {
	target := mustParseProbe(t, "1.98.0-nightly", "2026-06-24", "22.1.7")
	host := target
	host.VersionText = "rustc 1.98.0-nightly (different-commit 2026-06-24)"
	if err := validateCompilerIdentity(target, host); err == nil {
		t.Fatal("validateCompilerIdentity() accepted different version text")
	}
}
