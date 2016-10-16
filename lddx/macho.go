package lddx

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	mhMagic   = 0xfeedface
	mhCigam   = 0xcefaedfe
	mhMagic64 = 0xfeedfacf
	mhCigam64 = 0xcffaedfe
	fatMagic  = 0xcafebabe
	fatCigam  = 0xbebafeca

	loadCmdReq       = 0x80000000
	loadCmdWeakDylib = (0x18 | loadCmdReq)
	loadCmdId        = 0x0d
)

type ArchType struct {
	Cpu    macho.Cpu // Architecture type (e.g. PPC, i386, amd64, arm)
	SubCpu uint32    // ???
}

type Dylib struct {
	Path           string    // The path to the library
	Time           uint32    // Time of library
	CurrentVersion uint32    // Library version
	CompatVersion  uint32    // Compatibility version
	Weak           bool      // Whether this is a weakly loaded library
	Arch           *ArchType // Architecture type
}

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
		if err != nil && err != io.EOF {
			return false, err
		}
		return false, nil
	}

	magic := binary.LittleEndian.Uint32(bytes)

	return magic == mhMagic || magic == mhCigam ||
		magic == mhMagic64 || magic == mhCigam64 ||
		magic == fatMagic || magic == fatCigam, nil
}

// FindFatMachOFiles will recursively search the specified folder for
// Fat files or Mach-O files. Symlinked folders are ignored.
func FindFatMachOFiles(folder string) ([]string, error) {
	var ret []string
	walkFn := func(path string, info os.FileInfo, err error) error {
		if (info.Mode() & os.ModeSymlink) != 0 {
			if stat, err := os.Stat(path); err != nil {
				LogWarn("Could not check symlink %s: %s", path, err)
				return nil
			} else if stat.IsDir() {
				LogNote("Skipping over symlink'ed dir: %s", path)
				return nil
			}
		} else if info.IsDir() {
			return nil
		}

		if isfm, err := IsFatMachO(path); err != nil {
			LogWarn("Could not check %s: %s", path, err)
		} else if isfm {
			LogInfo("Found Fat/Mach-O: %s", path)
			ret = append(ret, path)
		}
		return nil
	}

	err := filepath.Walk(folder, walkFn)
	return ret, err
}

// TryParseLoadCmd attempts to read information about a given load command.
// This code is based on the LoadCmdDylib loader code in debug/macho.
func TryParseLoadCmd(loadCmd macho.LoadCmd, data []byte, byteOrder binary.ByteOrder) (*Dylib, error) {
	loadCommand := macho.LoadCmd(byteOrder.Uint32(data[0:4]))

	// Check if this is the given load command, otherwise ignore.
	if loadCommand != loadCmd {
		return nil, nil
	}

	var header macho.DylibCmd
	b := bytes.NewReader(data)
	if err := binary.Read(b, byteOrder, &header); err != nil {
		return nil, err
	} else if header.Name >= uint32(len(data)) {
		return nil, errors.New("invalid name in dynamic library command")
	}

	strEnd := int(header.Name)
	for strEnd < len(data) && data[strEnd] != 0 {
		strEnd++
	}

	return &Dylib{
		Path:           string(data[header.Name:strEnd]),
		Time:           header.Time,
		CurrentVersion: header.CurrentVersion,
		CompatVersion:  header.CompatVersion,
		Weak:           true,
	}, nil
}

// ReadDylibs returns the list of dynamic libraries referenced by a file.
// The file may either be a fat file or a normal Mach-O file.
// This method will search for both normal libs and weakly loaded libs.
func ReadDylibs(file string, limiter chan int) ([]Dylib, error) {
	var libs []*macho.File

	if limiter != nil {
		<-limiter
		defer func() { limiter <- 1 }()
	}

	if fp, err := macho.Open(file); err != nil {
		if fat, err := macho.OpenFat(file); err != nil {
			return nil, err
		} else {
			for _, lib := range fat.Arches {
				libs = append(libs, lib.File)
			}
			defer fat.Close()
		}
	} else {
		defer fp.Close()
		libs = append(libs, fp)
	}

	var ret []Dylib
	for _, lib := range libs {
		arch := ArchType{
			Cpu:    lib.Cpu,
			SubCpu: lib.SubCpu,
		}

		for _, load := range lib.Loads {
			if dyl, ok := load.(*macho.Dylib); ok {
				ret = append(ret, Dylib{
					Path:           dyl.Name,
					Time:           dyl.Time,
					CurrentVersion: dyl.CurrentVersion,
					CompatVersion:  dyl.CompatVersion,
					Weak:           false,
					Arch:           &arch,
				})
			} else if dl, err := TryParseLoadCmd(loadCmdWeakDylib, load.Raw(), lib.ByteOrder); err != nil {
				return nil, err
			} else if dl != nil {
				dl.Arch = &arch
				ret = append(ret, *dl)
			}
		}
	}
	return ret, nil
}

// GetDylibInfo gets information about the file itself, if available.
// For example, if the file is a dylib, it returns information about the Dylib itself.
func GetDylibInfo(file string) ([]Dylib, error) {
	var libs []*macho.File

	if fp, err := macho.Open(file); err != nil {
		if fat, err := macho.OpenFat(file); err != nil {
			return nil, err
		} else {
			for _, lib := range fat.Arches {
				libs = append(libs, lib.File)
			}
			defer fat.Close()
		}
	} else {
		defer fp.Close()
		libs = append(libs, fp)
	}

	var ret []Dylib
	for _, lib := range libs {
		arch := ArchType{
			Cpu:    lib.Cpu,
			SubCpu: lib.SubCpu,
		}

		for _, load := range lib.Loads {
			if dl, err := TryParseLoadCmd(loadCmdId, load.Raw(), lib.ByteOrder); err != nil {
				return nil, err
			} else if dl != nil {
				dl.Arch = &arch
				ret = append(ret, *dl)
				break
			}
		}
	}
	return ret, nil
}
