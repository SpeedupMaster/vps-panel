// Package scripts embeds the install script served at /install.sh.
package scripts

import _ "embed"

//go:embed install.sh
var Install []byte
