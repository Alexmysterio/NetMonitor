//go:build !linux

package main

import "fmt"

func restartSelf(string) error {
	return fmt.Errorf("la actualización en caliente solo está soportada en Linux")
}
