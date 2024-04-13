package main

import "testing"

// TestFCBFromString is a trivial test to only cover the basics right now.
func TestFCBFromString(t *testing.T) {

	// Simple test to ensure the basic one works.
	f := FCBFromString("b:foo")
	if f.Drive != 1 {
		t.Fatalf("drive wrong")
	}
	if f.GetName() != "FOO" {
		t.Fatalf("name wrong, got '%v'", f.GetName())
	}
	if f.GetType() != "" {
		t.Fatalf("unexpected suffix '%v'", f.GetType())
	}

	// Try a long name, to confirm it is truncated
	f = FCBFromString("c:this-is-a-long-name")
	if f.Drive != 2 {
		t.Fatalf("drive wrong")
	}
	if f.GetName() != "THIS-IS-" {
		t.Fatalf("name wrong, got '%v'", f.GetName())
	}
	if f.GetType() != "" {
		t.Fatalf("unexpected suffix '%v'", f.GetType())
	}

}
