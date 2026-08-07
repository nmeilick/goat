package main

import "testing"

func TestGreet(t *testing.T) {
	if greet() != "hello" {
		t.Fail()
	}
}
