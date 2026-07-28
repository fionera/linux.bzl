package main

import (
	"reflect"
	"testing"

	"github.com/hermeticbuild/linux.bzl/internal/rusttoolchain"
)

func TestApplyPredicates(t *testing.T) {
	probe := rusttoolchain.Probe{
		Schema:          rusttoolchain.ProbeSchema,
		Release:         "1.98.0-nightly",
		Semver:          "1.98.0",
		Channel:         "nightly",
		LLVMVersion:     "22.1.7",
		VersionCode:     109800,
		LLVMVersionCode: 220107,
	}
	got, err := applyPredicates(probe, []string{"rustc", "--old", "--keep"}, []versionPredicate{{
		AtLeast:    "1.98.0",
		Add:        []string{"--new"},
		Remove:     []string{"--old"},
		ElseAdd:    []string{"--fallback"},
		ElseRemove: []string{"--keep"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rustc", "--keep", "--new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestApplyPredicatesElse(t *testing.T) {
	probe := rusttoolchain.Probe{
		Schema:          rusttoolchain.ProbeSchema,
		Release:         "1.78.0",
		Semver:          "1.78.0",
		Channel:         "stable",
		LLVMVersion:     "18.1.2",
		VersionCode:     107800,
		LLVMVersionCode: 180102,
	}
	got, err := applyPredicates(probe, []string{"rustc", "--new"}, []versionPredicate{{
		AtLeast:    "1.98.0",
		Add:        []string{"--current"},
		Remove:     []string{"--old"},
		ElseAdd:    []string{"--old"},
		ElseRemove: []string{"--new"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rustc", "--old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}
