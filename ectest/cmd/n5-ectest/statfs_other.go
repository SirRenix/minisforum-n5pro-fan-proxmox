//go:build !linux

package main

import "errors"

func freeBytes(string) (uint64, error) { return 0, errors.New("statfs: linux only") }
