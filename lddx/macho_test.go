package lddx

import (
	"path/filepath"
	"testing"
)

type isFatMachOTest struct {
	file           string
	expectedResult bool
	expectError    bool
}

func TestIsFatMachO(t *testing.T) {
	testcases := []isFatMachOTest{
		{file: "macho01", expectedResult: true, expectError: false},
		{file: "macho02", expectedResult: true, expectError: false},
		{file: "macho03", expectedResult: true, expectError: false},
		{file: "macho04", expectedResult: true, expectError: false},
		{file: "macho05", expectedResult: true, expectError: false},
		{file: "macho06", expectedResult: true, expectError: false},
		{file: "macho07", expectedResult: false, expectError: false},
		{file: "macho08", expectedResult: false, expectError: true},
		{file: "someNonExistentFile", expectedResult: false, expectError: true},
	}

	for _, test := range testcases {
		result, err := IsFatMachO(filepath.Join("testdata", test.file))
		if err != nil && !test.expectError {
			t.Errorf("File %s: Unexpected error: %s", test.file, err)
		} else if err == nil && test.expectError {
			t.Errorf("File %s: Expected error but got nil", test.file)
		} else if result != test.expectedResult {
			t.Errorf("File %s: Expected result %v but got %v", test.file, test.expectedResult, result)
		}
	}
}
