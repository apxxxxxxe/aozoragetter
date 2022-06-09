package main

import "testing"

func TestMain(m *testing.M) {
	args := []string{
		"test",
		"杜子春",
		"蜘蛛の糸",
	}
	sub(args)
}
