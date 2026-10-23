package airplay

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"

	"howett.net/plist"
)

var ErrInvalidReceiverPlist = errors.New("invalid receiver property list")

const (
	receiverPlistBytes   = 8 << 20
	receiverPlistObjects = 65536
	receiverPlistDepth   = 64
	receiverPlistBudget  = 16 << 20
)

// The upstream binary decoder trusts extended lengths and follows references
// recursively. Validate those allocations and the graph before calling it;
// recover is only the final boundary for other malformed receiver data.
func unmarshalReceiverPlist(data []byte, target any) (format int, err error) {
	defer func() {
		if recover() != nil {
			format, err = 0, ErrInvalidReceiverPlist
		}
	}()
	if !validReceiverPlist(data) {
		return 0, ErrInvalidReceiverPlist
	}
	format, err = plist.Unmarshal(data, target)
	if err != nil {
		return 0, ErrInvalidReceiverPlist
	}
	return format, nil
}

type receiverPlistObject struct {
	weight uint64
	refs   []uint64
}

func plistBigEndian(data []byte) uint64 {
	var value uint64
	for _, octet := range data {
		value = value<<8 | uint64(octet)
	}
	return value
}

func validReceiverPlist(data []byte) bool {
	if len(data) > receiverPlistBytes {
		return false
	}
	if !bytes.HasPrefix(data, []byte("bplist00")) {
		return validReceiverXMLPlist(data)
	}
	if len(data) < 41 {
		return false
	}
	trailer := data[len(data)-32:]
	offsetSize, refSize := uint64(trailer[6]), uint64(trailer[7])
	count := binary.BigEndian.Uint64(trailer[8:16])
	top := binary.BigEndian.Uint64(trailer[16:24])
	table := binary.BigEndian.Uint64(trailer[24:32])
	if offsetSize < 1 || offsetSize > 8 || refSize < 1 || refSize > 8 || count < 1 || count > receiverPlistObjects || top >= count || table < 8 || table > uint64(len(data)-32) {
		return false
	}
	if count > (uint64(len(data)-32)-table)/offsetSize {
		return false
	}
	objects := make([]receiverPlistObject, int(count))
	totalReferences := uint64(0)
	for index := uint64(0); index < count; index++ {
		at := table + index*offsetSize
		position := plistBigEndian(data[at : at+offsetSize])
		if position < 8 || position >= table {
			return false
		}
		marker := data[position]
		position++
		kind, size := marker>>4, uint64(marker&15)
		if (kind >= 4 && kind <= 6 || kind >= 10 && kind <= 13) && size == 15 {
			if position >= table || data[position]>>4 != 1 || data[position]&15 > 3 {
				return false
			}
			width := uint64(1) << (data[position] & 15)
			position++
			if width > table-position {
				return false
			}
			size = plistBigEndian(data[position : position+width])
			position += width
		}
		unit, references := uint64(1), uint64(0)
		switch kind {
		case 0:
			if marker != 0 && marker != 8 && marker != 9 && marker != 15 {
				return false
			}
			size = 0
		case 1:
			if size > 4 {
				return false
			}
			size = uint64(1) << size
		case 2:
			if size != 2 && size != 3 {
				return false
			}
			size = uint64(1) << size
		case 3:
			if size != 3 {
				return false
			}
			size = 8
		case 4, 5:
		case 6:
			unit = 2
		case 8:
			size++
		case 10, 12:
			unit, references = refSize, size
		case 13:
			unit = refSize * 2
			if size > receiverPlistObjects {
				return false
			}
			references = size * 2
		default:
			return false
		}
		if size > (table-position)/unit || references > receiverPlistObjects {
			return false
		}
		totalReferences += references
		if totalReferences > receiverPlistObjects*2 {
			return false
		}
		object := receiverPlistObject{weight: 1 + size*unit}
		for ref := uint64(0); ref < references; ref++ {
			at := position + ref*refSize
			value := plistBigEndian(data[at : at+refSize])
			if value >= count {
				return false
			}
			object.refs = append(object.refs, value)
		}
		objects[index] = object
	}
	marks, weights := make([]byte, len(objects)), make([]uint64, len(objects))
	heights := make([]int, len(objects))
	var visit func(uint64, int) (uint64, bool)
	visit = func(index uint64, depth int) (uint64, bool) {
		if depth > receiverPlistDepth || marks[index] == 1 {
			return 0, false
		}
		if marks[index] == 2 {
			return weights[index], depth+heights[index] <= receiverPlistDepth
		}
		marks[index] = 1
		weight := objects[index].weight
		for _, reference := range objects[index].refs {
			child, ok := visit(reference, depth+1)
			if !ok || child > receiverPlistBudget || weight > receiverPlistBudget-child {
				return 0, false
			}
			weight += child
			if height := heights[reference] + 1; height > heights[index] {
				heights[index] = height
			}
		}
		marks[index], weights[index] = 2, weight
		return weight, weight <= receiverPlistBudget && depth+heights[index] <= receiverPlistDepth
	}
	for index := range objects {
		if _, ok := visit(uint64(index), 0); !ok {
			return false
		}
	}
	return true
}

func validReceiverXMLPlist(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, tokens, roots := 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return roots == 1 && depth == 0
		}
		if err != nil {
			return false
		}
		tokens++
		if tokens > receiverPlistObjects*2 {
			return false
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || value.Name.Local != "plist" {
					return false
				}
			}
			depth++
			if depth > receiverPlistDepth {
				return false
			}
		case xml.EndElement:
			depth--
		}
	}
}
