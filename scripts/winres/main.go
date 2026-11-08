//go:build windows

// Asset embedding for the elevated helper. This is the RT_MANIFEST-only
// subset of MyGo's MIT-licensed cmd/mygo/winres.go, so CI needs no MinGW.
//
// MIT License
// Copyright (c) 2026 MyGo contributors
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
package main

import (
	"bytes"
	"encoding/binary"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: go run ./scripts/winres <manifest.xml> <output.syso>")
	}
	manifest, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[2], manifestSyso(manifest), 0o644); err != nil {
		log.Fatal(err)
	}
}

// manifestSyso emits an amd64 COFF .rsrc section containing resource type 24
// (RT_MANIFEST), resource ID 1, language en-US. The Go linker includes .syso
// files from the main package, so the helper requests UAC before its UI-free
// process starts.
func manifestSyso(manifest []byte) []byte {
	le := binary.LittleEndian
	const entryAt, dataAt = 72, 88
	raw := make([]byte, dataAt+len(manifest))
	// Directory headers have four skipped uint32 fields and two uint16 counts
	// at offsets 12 and 14. The last is the number of named integer IDs.
	for _, at := range []int{0, 24, 48} {
		le.PutUint16(raw[at+14:], 1)
	}
	le.PutUint32(raw[20:], 24|0x80000000) // type: RT_MANIFEST
	le.PutUint32(raw[40:], 1)             // resource ID
	le.PutUint32(raw[44:], 48|0x80000000) // language directory
	le.PutUint32(raw[64:], 0x409)         // en-US
	le.PutUint32(raw[68:], entryAt)       // resource data entry
	le.PutUint32(raw[entryAt:], dataAt)   // section-relative address
	le.PutUint32(raw[entryAt+4:], uint32(len(manifest)))
	copy(raw[dataAt:], manifest)
	for len(raw)%4 != 0 {
		raw = append(raw, 0)
	}

	var out bytes.Buffer
	const headerSize, sectionHeaderSize = 20, 40
	rawAt := headerSize + sectionHeaderSize
	relocAt := rawAt + len(raw)
	symbolsAt := relocAt + 10 // one IMAGE_RELOCATION
	_ = binary.Write(&out, le, struct {
		Machine, Sections                   uint16
		Time, Symbols, SymbolCount          uint32
		OptionalHeaderSize, Characteristics uint16
	}{0x8664, 1, 0, uint32(symbolsAt), 1, 0, 0})
	_ = binary.Write(&out, le, struct {
		Name                                        [8]byte
		VirtualSize, VirtualAddress, RawSize, RawAt uint32
		RelocAt, LineNumbersAt                      uint32
		Relocs, LineNumbers                         uint16
		Characteristics                             uint32
	}{[8]byte{'.', 'r', 's', 'r', 'c'}, 0, 0, uint32(len(raw)), uint32(rawAt), uint32(relocAt), 0, 1, 0, 0x40000040})
	out.Write(raw)
	_ = binary.Write(&out, le, uint32(entryAt))
	_ = binary.Write(&out, le, uint32(0)) // symbol 0: the section
	_ = binary.Write(&out, le, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	out.Write([]byte{'.', 'r', 's', 'r', 'c', 0, 0, 0})
	_ = binary.Write(&out, le, uint32(0))
	_ = binary.Write(&out, le, uint16(1)) // SectionNumber
	_ = binary.Write(&out, le, uint16(0)) // Type
	out.Write([]byte{3, 0})               // static class, no aux symbols
	_ = binary.Write(&out, le, uint32(4)) // empty string table
	return out.Bytes()
}
