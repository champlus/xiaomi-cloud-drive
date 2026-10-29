//go:build !darwin

package keychain

import "errors"

func Save([]byte) error     { return errors.New("钥匙串仅适用于 macOS") }
func Load() ([]byte, error) { return nil, nil }
func Delete() error         { return nil }
