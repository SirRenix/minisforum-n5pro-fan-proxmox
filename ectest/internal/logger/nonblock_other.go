//go:build !linux

package logger

import "errors"

func kmsgRecords(string) ([]string, error) { return nil, errors.New("kmsg: linux only") }
