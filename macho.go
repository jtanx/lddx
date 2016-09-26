package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

const MH_MAGIC = 0xfeedface
const MH_CIGAM = 0xcefaedfe
const MH_MAGIC_64 = 0xfeedfacf
const MH_CIGAM_64 = 0xcffaedfe
const FAT_MAGIC = 0xcafebabe
const FAG_CIGAM = 0xbebafeca

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

	return magic == MH_MAGIC || magic == MH_CIGAM ||
		magic == MH_MAGIC_64 || magic == MH_CIGAM_64 ||
		magic == FAT_MAGIC || magic == FAG_CIGAM, nil
}
