package main

import (
	"fmt"

	_ "vpspanel/internal/common/proto"
	"vpspanel/internal/common/version"
)

func main() {
	fmt.Println("vpsagent", version.Version)
}
