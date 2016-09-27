package lddx

import (
	"encoding/binary"
	"fmt"
	"os"
)

const mhMagic = 0xfeedface
const mhCigam = 0xcefaedfe
const mhMagic64 = 0xfeedfacf
const mhCigam64 = 0xcffaedfe
const fatMagic = 0xcafebabe
const fatCigam = 0xbebafeca

// IsFatMachO reads the first four bytes of the given file to
// determine if it is either a Mach-O or Universal (fat) file.
// On error, the result is false, and the error value is also returned.
func IsFatMachO(file string) (bool, error) {
	fp, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer fp.Close()

	bytes := make([]byte, 4)
	if num, err := fp.Read(bytes); num != 4 || err != nil {
		if err != nil {
			return false, err
		}
		return false, fmt.Errorf("Could not read magic header")
	}

	magic := binary.LittleEndian.Uint32(bytes)

	return magic == mhMagic || magic == mhCigam ||
		magic == mhMagic64 || magic == mhCigam64 ||
		magic == fatMagic || magic == fatCigam, nil
}
