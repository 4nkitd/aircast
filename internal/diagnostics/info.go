package diagnostics

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

var errInvalidInfo = errors.New("invalid AirPlay information property list")

func binaryModel(data []byte) (string, error) {
	if len(data) < 40 {
		return "", errInvalidInfo
	}
	trailer := data[len(data)-32:]
	offsetSize, refSize := uint64(trailer[6]), uint64(trailer[7])
	count := binary.BigEndian.Uint64(trailer[8:16])
	root := binary.BigEndian.Uint64(trailer[16:24])
	table := binary.BigEndian.Uint64(trailer[24:32])
	if offsetSize < 1 || offsetSize > 8 || refSize < 1 || refSize > 8 || count < 1 || count > 65536 || root >= count || table < 8 || table > uint64(len(data)-32) || count > (uint64(len(data)-32)-table)/offsetSize {
		return "", errInvalidInfo
	}
	read := func(start, size uint64) (uint64, bool) {
		if size > 8 || start > uint64(len(data)) || size > uint64(len(data))-start {
			return 0, false
		}
		var value uint64
		for _, b := range data[start : start+size] {
			value = value<<8 | uint64(b)
		}
		return value, true
	}
	offset := func(index uint64) (uint64, bool) {
		if index >= count {
			return 0, false
		}
		value, ok := read(table+index*offsetSize, offsetSize)
		return value, ok && value >= 8 && value < table
	}
	length := func(position uint64) (uint64, uint64, bool) {
		if position >= table {
			return 0, 0, false
		}
		n := uint64(data[position] & 15)
		position++
		if n != 15 {
			return position, n, true
		}
		if position >= table || data[position]>>4 != 1 || data[position]&15 > 3 {
			return 0, 0, false
		}
		size := uint64(1) << (data[position] & 15)
		position++
		if size > table-position {
			return 0, 0, false
		}
		n, ok := read(position, size)
		return position + size, n, ok
	}
	text := func(index uint64) (string, error) {
		position, ok := offset(index)
		if !ok {
			return "", errInvalidInfo
		}
		kind := data[position] >> 4
		start, n, ok := length(position)
		if !ok || n > 4096 || kind != 5 && kind != 6 && kind != 7 {
			return "", errInvalidInfo
		}
		unit := uint64(1)
		if kind == 6 {
			unit = 2
		}
		if start > table || n > (table-start)/unit {
			return "", errInvalidInfo
		}
		if kind != 6 {
			return string(data[start : start+n]), nil
		}
		runes := make([]uint16, n)
		for k := range runes {
			runes[k] = binary.BigEndian.Uint16(data[start+uint64(k)*2:])
		}
		return string(utf16.Decode(runes)), nil
	}
	position, ok := offset(root)
	if !ok || data[position]>>4 != 13 {
		return "", errInvalidInfo
	}
	start, n, ok := length(position)
	if !ok || n > 4096 || start > table || n > (table-start)/(refSize*2) {
		return "", errInvalidInfo
	}
	for k := uint64(0); k < n; k++ {
		keyRef, _ := read(start+k*refSize, refSize)
		key, err := text(keyRef)
		if err != nil {
			return "", err
		}
		if key == "model" {
			valueRef, _ := read(start+(n+k)*refSize, refSize)
			return text(valueRef)
		}
	}
	return "", nil
}

func xmlModel(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	rootSeen, dictSeen := false, false
	var key, model string
	var text strings.Builder
	capture := ""
	for tokens := 0; tokens < 32768; tokens++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !rootSeen || !dictSeen || depth != 0 {
				return "", errInvalidInfo
			}
			return model, nil
		}
		if err != nil {
			return "", errInvalidInfo
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 64 || capture != "" {
				return "", errInvalidInfo
			}
			if depth == 1 {
				if rootSeen || value.Name.Local != "plist" {
					return "", errInvalidInfo
				}
				rootSeen = true
			} else if depth == 2 {
				if dictSeen || value.Name.Local != "dict" {
					return "", errInvalidInfo
				}
				dictSeen = true
			} else if depth == 3 && value.Name.Local == "key" {
				capture = "key"
				text.Reset()
			} else if depth == 3 && key == "model" {
				if value.Name.Local != "string" {
					return "", errInvalidInfo
				}
				capture = "model"
				text.Reset()
			}
		case xml.EndElement:
			if depth == 3 {
				if capture == "key" {
					key = text.String()
				} else {
					if capture == "model" {
						model = text.String()
					}
					key = ""
				}
				capture = ""
			}
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return "", errInvalidInfo
			}
			if capture != "" {
				if text.Len()+len(value) > 4096 {
					return "", errInvalidInfo
				}
				text.Write(value)
			}
		}
	}
	return "", errInvalidInfo
}
