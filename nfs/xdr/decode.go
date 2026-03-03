// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause

package xdr

import (
	"fmt"
	"io"

	xdr "github.com/rasky/go-xdr/xdr2"
)

func Read(r io.Reader, val interface{}) error {
	_, err := xdr.Unmarshal(r, val)
	return err
}

func ReadUint32(r io.Reader) (uint32, error) {
	var n uint32
	if err := Read(r, &n); err != nil {
		return n, err
	}

	return n, nil
}

// MaxOpaqueSize is the maximum allowed length for ReadOpaque to prevent
// memory exhaustion attacks from malicious servers.
const MaxOpaqueSize = 1024 * 1024 // 1MB max

func ReadOpaque(r io.Reader) ([]byte, error) {
	length, err := ReadUint32(r)
	if err != nil {
		return nil, err
	}

	if length > MaxOpaqueSize {
		return nil, fmt.Errorf("opaque length %d exceeds maximum %d", length, MaxOpaqueSize)
	}

	buf := make([]byte, length)
	if _, err = io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	return buf, nil
}

// MaxReadLength is the maximum allowed length for ReadUint32List to prevent
// memory exhaustion attacks from malicious servers.
const MaxReadLength = 1024 * 1024 // 1M elements max

func ReadUint32List(r io.Reader) ([]uint32, error) {
	length, err := ReadUint32(r)
	if err != nil {
		return nil, err
	}

	if length > MaxReadLength {
		return nil, fmt.Errorf("xdr: ReadUint32List length %d exceeds maximum %d", length, MaxReadLength)
	}

	buf := make([]uint32, length)

	for i := 0; i < int(length); i++ {
		buf[i], err = ReadUint32(r)
		if err != nil {
			return nil, err
		}
	}

	return buf, nil
}
