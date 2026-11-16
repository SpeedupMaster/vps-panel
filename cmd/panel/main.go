package main

import (
	"fmt"

	"vpspanel/internal/common/version"
)

func main() {
	fmt.Println("vps-panel", version.Version)
}
