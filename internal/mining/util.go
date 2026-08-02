package mining

import (
	"encoding/hex"
	"fmt"
)

// hexEncode encodes b as a hexadecimal string.
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

func makeErr(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}
