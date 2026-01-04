// Package lite3 is an implementation of the Lite³ serialization format.
package lite3

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"
	"unsafe"
)

const (
	nodeSize = 96
)

// Value types.
const (
	TypeNull = iota
	TypeBool
	TypeInt
	TypeFloat
	TypeBytes
	TypeString
	TypeObject
	TypeArray
	TypeInvalid
)

var typeSizes = [...]int{
	TypeNull:    0,
	TypeBool:    1,
	TypeInt:     8,
	TypeFloat:   8,
	TypeBytes:   4,
	TypeString:  4,
	TypeObject:  nodeSize - 1, // type byte is treeNode.typ
	TypeArray:   nodeSize - 1,
	TypeInvalid: 0,
}

func unsafeString(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }
func unsafeSlice(s string) []byte  { return unsafe.Slice(unsafe.StringData(s), len(s)) }

func keyHash(key string) uint32 {
	// DJB2
	hash := uint32(5381)
	for i := range key {
		hash = ((hash << 5) + hash) + uint32(key[i])
	}
	return hash
}

// A ObjectKey is a key paired with its precomputed hash.
type ObjectKey struct {
	key  string
	hash uint32
}

// Key returns the ObjectKey for the given string.
func Key(key string) ObjectKey {
	return ObjectKey{key: key, hash: keyHash(key)}
}

// A Buffer is a serialized B-tree.
type Buffer struct {
	buf []byte
}

func (b *Buffer) grow(n int) {
	if len(b.buf)+n > cap(b.buf) {
		b.buf = append(b.buf, make([]byte, n)...)
	} else {
		b.buf = b.buf[:len(b.buf)+n]
	}
}

type cNode struct {
	typeGen  uint32 // packed 8 bit type + 24 bit generation
	hashes   [7]uint32
	kcSize   uint32 // packed 8 bit key count + 24 bit size
	kvOff    [7]uint32
	childOff [8]uint32
}

var _ [nodeSize]struct{} = [unsafe.Sizeof(cNode{})]struct{}{}

type treeNode struct {
	b   *Buffer
	off uint32 // ptr to cNode
}

func (b *Buffer) allocNode() treeNode {
	b.grow(((len(b.buf) + 3) & ^3) - len(b.buf))
	b.grow(nodeSize)
	off := uint32(len(b.buf) - nodeSize)
	clear(b.buf[off:])
	return b.treeNode(off)
}

func (b *Buffer) treeNode(off uint32) treeNode {
	return treeNode{b: b, off: off}
}

func (node treeNode) ptr() *cNode {
	return (*cNode)(unsafe.Pointer(&node.b.buf[node.off]))
}

func (node treeNode) setType(typ uint8) {
	node.b.buf[node.off] = typ
}

func (node treeNode) getType() uint8 {
	return node.b.buf[node.off]
}

func (node treeNode) keyCount() uint8 {
	return uint8(node.ptr().kcSize & 7)
}

func (node treeNode) setKeyCount(kc uint8) {
	p := node.ptr()
	p.kcSize = (p.kcSize &^ 7) | uint32(kc&7)
}

func (node treeNode) size() uint32 {
	return node.ptr().kcSize >> 6
}

func (node treeNode) setSize(size uint32) {
	p := node.ptr()
	p.kcSize = uint32(node.keyCount()) | (size << 6)
}

func (node treeNode) incGen() {
	p := node.ptr()
	p.typeGen = uint32(node.getType()) | (p.typeGen + (1 << 8))
}

func (node treeNode) getGen() uint32 {
	return node.ptr().typeGen >> 8
}

func (node treeNode) isFull() bool {
	return node.keyCount() == uint8(len(cNode{}.hashes))
}

func (node treeNode) hasChildren() bool {
	// The "correct" check is node.childOff == ([8]uint8{}), but since:
	//   a) children are stored in sorted order, and
	//   b) the only node with an offset of 0 is the buffer root, and
	//   c) that node cannot be a child of any other node,
	// this is equivalent.
	return node.ptr().childOff[0] != 0
}

func (node treeNode) child(i uint8) treeNode {
	return node.b.treeNode(node.ptr().childOff[i])
}

func (node treeNode) slot(hash uint32) (i uint8, exists bool) {
	// NOTE: equivalent to slices.BinarySearch(node.hashes[:node.keyCount], hash)
	h := node.ptr().hashes[:node.keyCount()]
	for i < uint8(len(h)) && h[i] < hash {
		i++
	}
	return i, i < uint8(len(h)) && h[i] == hash
}

func (node treeNode) insertKV(i uint8, hash uint32, off uint32) {
	p := node.ptr()
	kc := node.keyCount()
	copy(p.hashes[i+1:], p.hashes[i:kc])
	copy(p.kvOff[i+1:], p.kvOff[i:kc])
	p.hashes[i] = hash
	p.kvOff[i] = off
	node.setKeyCount(kc + 1)
}

func (node treeNode) insertChild(i uint8, child treeNode) {
	p := node.ptr()
	copy(p.childOff[i+1:], p.childOff[i:])
	p.childOff[i] = child.off
}

type valPtr struct {
	b   *Buffer
	off uint32
}

func (vp valPtr) len() int {
	typ := vp.b.buf[vp.off]
	n := typeSizes[typ]
	if typ == TypeString || typ == TypeBytes {
		n += int(binary.LittleEndian.Uint32(vp.b.buf[vp.off+1:]))
	}
	return n
}

func (vp valPtr) clear() {
	clear(vp.b.buf[vp.off:][:1+vp.len()])
}

func (vp valPtr) toValue() (v Value) {
	if vp.b == nil {
		return Value{Type: TypeInvalid}
	}
	buf := vp.b.buf[vp.off:]
	v.Type = buf[0]
	switch v.Type {
	case TypeInvalid, TypeNull:
	case TypeBool:
		v.num = uint64(buf[1])
	case TypeInt, TypeFloat:
		v.num = binary.LittleEndian.Uint64(buf[1:])
	case TypeString, TypeBytes:
		n := binary.LittleEndian.Uint32(buf[1:])
		v.str = buf[1+4:][:n-1] // exclude C string terminator
	case TypeObject, TypeArray:
		v.c = container{treeNode{vp.b, vp.off}}
	default:
		panic("unhandled type")
	}
	return
}

func (node treeNode) kv(i uint8) ([]byte, valPtr) {
	off := node.ptr().kvOff[i]
	buf := node.b.buf[off:]
	var key []byte
	if node.getType() == TypeObject {
		tagSize := uint32(buf[0]&3) + 1
		keySize := binary.LittleEndian.Uint32(buf) & ((1 << (8 * tagSize)) - 1) >> 2
		key = buf[tagSize:][:keySize-1] // exclude C string terminator
		off += tagSize + keySize
		buf = buf[tagSize+keySize:]
	}
	return key, valPtr{node.b, off}
}

func appendVal(buf []byte, valType uint8, val []byte) []byte {
	buf = append(buf, valType)
	if valType == TypeString || valType == TypeBytes {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(val)+1))
		buf = append(buf, val...)
		buf = append(buf, 0) // C string terminator
		return buf
	}
	return append(buf, val...)
}

func (b *Buffer) appendKV(key string, valType uint8, val []byte) (uint32, uint32) {
	var keyTagSize int
	switch {
	case len(key) < 1<<6:
		keyTagSize = 1
	case len(key) < 1<<14:
		keyTagSize = 2
	default:
		keyTagSize = 3
	}

	// treeNodes must be aligned
	if valType == TypeObject || valType == TypeArray {
		unalignedValOffset := len(b.buf) + keyTagSize + len(key)
		alignedValOffset := (unalignedValOffset + 3) & ^3
		b.grow(alignedValOffset - unalignedValOffset)
	}

	kvOff := uint32(len(b.buf))
	if key != "" {
		tagSize := keyTagSize
		b.buf = binary.LittleEndian.AppendUint32(b.buf, uint32(((len(key)+1)<<2)|(tagSize-1)))
		b.buf = b.buf[:len(b.buf)-4+tagSize]
		b.buf = append(b.buf, []byte(key)...)
		b.buf = append(b.buf, 0) // C string terminator
	}
	typOff := uint32(len(b.buf))
	b.buf = appendVal(b.buf, valType, val)
	return kvOff, typOff
}

func (node treeNode) splitChild(i uint8) {
	// Move median into parent
	child := node.child(i)
	cptr := child.ptr()
	const med = uint8(len(cNode{}.hashes) / 2)
	node.insertKV(i, cptr.hashes[med], cptr.kvOff[med])

	// Move lower half of entries into new sibling
	sibling := node.b.allocNode()
	sibling.setType(node.getType())
	sibling.setKeyCount(med)
	copy(sibling.ptr().hashes[:med], cptr.hashes[med+1:])
	copy(sibling.ptr().kvOff[:med], cptr.kvOff[med+1:])
	copy(sibling.ptr().childOff[:med], cptr.childOff[med+1:])
	clear(cptr.hashes[med:])
	clear(cptr.kvOff[med:])
	clear(cptr.childOff[med:])
	child.setKeyCount(med)
	child.setSize(0)

	// record sibling in parent
	node.insertChild(i+1, sibling)
}

type container struct {
	node treeNode
}

func (c container) count() int   { return int(c.node.size()) }
func (c container) iter() *liter { return newLiter(c.node) }

func (c container) value(key string, keyHash uint32) Value {
	try := func(h uint32) (valPtr, bool) {
		node := c.node
		for {
			i, exists := node.slot(h)
			if exists {
				curKey, val := node.kv(i)
				if string(curKey) != key {
					return valPtr{}, false
				}
				return val, true
			} else if !node.hasChildren() {
				return valPtr{}, true // not found
			}
			node = node.child(i)
		}
	}

	for probe := uint32(0); ; probe++ {
		if vp, ok := try(keyHash + probe*probe); ok {
			return vp.toValue()
		}
	}
}

func (c container) set(key string, keyHash uint32, valType uint8, val []byte) uint32 {
	root := c.node
	root.incGen()
	b := root.b

	// Special case: root is full, and we don't have a parent to split into;
	// create a new parent to point to the current root.
	if root.isFull() {
		movedRoot := b.allocNode()
		copy(b.buf[movedRoot.off:], b.buf[root.off:])
		// root becomes parent
		root.setKeyCount(0)
		clear(root.ptr().hashes[:])
		clear(root.ptr().kvOff[:])
		clear(root.ptr().childOff[:])
		root.insertChild(0, movedRoot)
		root.splitChild(0)
	}

	try := func(h uint32) (uint32, bool) {
		node := root
		for {
			if i, exists := node.slot(h); exists {
				curKey, curVal := node.kv(i)
				if string(curKey) != key {
					return 0, false // collision
				} else if curVal.len() >= len(val) {
					// Reuse existing KV space
					curVal.clear()
					_ = appendVal(b.buf[:curVal.off], valType, val)
					return curVal.off, true
				}
				// Append new KV and wipe the old one
				curVal.clear()
				kvOff, typOff := b.appendKV(key, valType, val)
				node.ptr().kvOff[i] = kvOff
				return typOff, true
			} else if !node.hasChildren() {
				// Leaf node with capacity; insert here
				kvOff, typOff := b.appendKV(key, valType, val)
				node.insertKV(i, h, kvOff)
				// increment container count
				root.setSize(root.size() + 1)
				return typOff, true
			} else {
				// Descend into child (or split if full)
				if child := node.child(i); !child.isFull() {
					node = child
				} else {
					node.splitChild(i) // we'll descend on next iteration
				}
			}
		}
	}

	for probe := uint32(0); ; probe++ {
		if typOff, ok := try(keyHash + probe*probe); ok {
			return typOff
		}
	}
}

func (c container) setValue(key string, keyHash uint32, val Value) {
	if val.Type == TypeObject || val.Type == TypeArray {
		panic("cannot set object or array value with setValue; use SetObject or SetArray instead")
	}
	var buf [8]byte
	data := val.str
	switch val.Type {
	case TypeBool:
		binary.LittleEndian.PutUint64(buf[:], val.num)
		data = buf[:1]
	case TypeInt, TypeFloat:
		binary.LittleEndian.PutUint64(buf[:], val.num)
		data = buf[:]
	}
	c.set(key, keyHash, val.Type, data)
}

func (c container) setContainer(key string, keyHash uint32, typ uint8) container {
	off := c.set(key, keyHash, typ, make([]byte, nodeSize-1))
	return container{c.node.b.treeNode(off)}
}

func (c container) setObject(key string, keyHash uint32) Object {
	return Object{c.setContainer(key, keyHash, TypeObject)}
}

func (c container) setArray(key string, keyHash uint32) Array {
	return Array{c.setContainer(key, keyHash, TypeArray)}
}

// An Object maps string keys to Value fields.
type Object struct {
	c container
}

// NumFields returns the number of fields in the Object.
func (o Object) NumFields() int { return o.c.count() }

// Get retrieves the value associated with the given key. If the key does not
// exist, the value will have TypeInvalid.
func (o Object) Get(key ObjectKey) Value { return o.c.value(key.key, key.hash) }

// Set sets the value associated with the given key.
func (o Object) Set(key ObjectKey, val Value) { o.c.setValue(key.key, key.hash, val) }

// SetObject sets a field to an empty Object and returns it.
func (o Object) SetObject(key ObjectKey) Object { return o.c.setObject(key.key, key.hash) }

// SetArray sets a field to an empty Array and returns it.
func (o Object) SetArray(key ObjectKey) Array { return o.c.setArray(key.key, key.hash) }

// All returns an iterator over the Object's key-value pairs. The order of the
// pairs is not specified.
func (o Object) All() iter.Seq2[string, Value] {
	return func(yield func(string, Value) bool) {
		it := o.c.iter()
		var key string
		var index int
		var value Value
		for it.next(&key, &index, &value) {
			if !yield(key, value) {
				return
			}
		}
	}
}

// An Array is a collection of Values keyed by index.
type Array struct {
	c container
}

// Len returns the length of the array.
func (a Array) Len() int { return a.c.count() }

// Get retrieves the value at the given index. If the index is out of bounds,
// the value will have TypeInvalid.
func (a Array) Get(i int) Value { return a.c.value("", uint32(i)) }

// Set sets the value at the given index.
func (a Array) Set(i int, val Value) { a.c.setValue("", uint32(i), val) }

// SetObject sets the value at the given index to an empty Object and returns it.
func (a Array) SetObject(i int) Object { return a.c.setObject("", uint32(i)) }

// SetArray sets the value at the given index to an empty Array and returns it.
func (a Array) SetArray(i int) Array { return a.c.setArray("", uint32(i)) }

// All returns an iterator over the Array's index-value pairs, in order.
func (a Array) All() iter.Seq2[int, Value] {
	return func(yield func(int, Value) bool) {
		it := a.c.iter()
		var key string
		var index int
		var value Value
		for it.next(&key, &index, &value) {
			if !yield(index, value) {
				return
			}
		}
	}
}

// A Value can be stored in a Lite³ Object or Array.
type Value struct {
	Type uint8
	num  uint64
	str  []byte
	c    container
}

// Any returns the Value as an empty interface, panicking if the value's type is
// invalid. TypeNull is represented as nil.
func (v Value) Any() any {
	switch v.Type {
	case TypeNull:
		return nil
	case TypeBool:
		return v.Bool()
	case TypeInt:
		return v.Int()
	case TypeFloat:
		return v.Float64()
	case TypeBytes:
		return v.Bytes()
	case TypeString:
		return v.String()
	case TypeObject:
		return v.Object()
	case TypeArray:
		return v.Array()
	case TypeInvalid:
		panic("invalid")
	default:
		panic("unhandled type")
	}
}

// Bool returns the Value as a bool. If the Value is not TypeBool, the result is
// undefined.
func (v Value) Bool() bool { return v.num != 0 }

// Uint64 returns the Value as a uint64. If the Value is not TypeInt, the result
// is undefined.
func (v Value) Uint64() uint64 { return v.num }

// Int returns the Value as an int. If the Value is not TypeInt, the result is
// undefined.
func (v Value) Int() int { return int(v.Uint64()) }

// Float64 returns the Value as a float64. If the Value is not TypeFloat, the
// result is undefined.
func (v Value) Float64() float64 { return math.Float64frombits(v.Uint64()) }

// RawBytes returns the Value as a []byte. If the Value is not TypeBytes, the
// result is undefined. The returned slice must not be modified.
func (v Value) RawBytes() []byte { return v.str }

// Bytes returns the Value as a []byte. If the Value is not TypeBytes, the result is
// undefined.
func (v Value) Bytes() []byte { return slices.Clone(v.RawBytes()) }

// RawString returns the Value as a string. If the Value is not TypeString, the
// result is undefined. The returned string aliases the same slice as RawBytes.
func (v Value) RawString() string { return unsafeString(v.RawBytes()) }

// String returns the Value as a string. If the Value is not TypeString, it
// returns a human-readable representation of the Value.
func (v Value) String() string {
	switch v.Type {
	case TypeString:
		return string(v.RawBytes())
	case TypeObject:
		return "<object>"
	case TypeArray:
		return "<array>"
	case TypeInvalid:
		return "<invalid>"
	}
	return fmt.Sprint(v.Any())
}

// Object returns the Value as an Object. If the Value is not TypeObject, the
// result is undefined.
func (v Value) Object() Object { return Object{c: v.c} }

// Array returns the Value as an Array. If the Value is not TypeArray, the
// result is undefined.
func (v Value) Array() Array { return Array{c: v.c} }

// Null returns a Value of TypeNull.
func Null() Value { return Value{Type: TypeNull} }

// Bool returns a Value of TypeBool.
func Bool(b bool) Value {
	if b {
		return Value{Type: TypeBool, num: 1}
	}
	return Value{Type: TypeBool, num: 0}
}

// Uint64 returns a Value of TypeInt.
func Uint64(u uint64) Value { return Value{Type: TypeInt, num: u} }

// Int returns a Value of TypeInt.
func Int(i int) Value { return Uint64(uint64(i)) }

// Float64 returns a Value of TypeFloat.
func Float64(f float64) Value { return Value{Type: TypeFloat, num: math.Float64bits(f)} }

// Bytes returns a Value of TypeBytes.
func Bytes(b []byte) Value { return Value{Type: TypeBytes, str: b} }

// String returns a Value of TypeString.
func String(s string) Value { return Value{Type: TypeString, str: unsafeSlice(s)} }

type liter struct {
	gen       uint32
	nodes     [10]treeNode
	depth     uint8
	nodeIndex [10]uint8
	err       error
}

func newLiter(root treeNode) *liter {
	it := &liter{
		gen: root.getGen(),
	}
	it.nodes[0] = root

	node := root
	for node.hasChildren() {
		node = node.child(0)
		if it.depth++; it.depth == uint8(len(it.nodes)) {
			it.err = errors.New("max depth exceeded")
			return nil
		}
		it.nodes[it.depth] = node
		it.nodeIndex[it.depth] = 0
	}
	return it
}

func (it *liter) next(key *string, index *int, value *Value) bool {
	if it.err != nil {
		return false
	} else if it.gen != it.nodes[0].getGen() {
		return false
	}
	node := it.nodes[it.depth]
	if !(node.getType() == TypeObject || node.getType() == TypeArray) {
		it.err = errors.New("not an array or object type")
		return false
	} else if it.depth == 0 && it.nodeIndex[it.depth] == node.keyCount() {
		return false
	}

	kvKey, kvVal := node.kv(it.nodeIndex[it.depth])
	*key = unsafeString(kvKey)
	*index = int(node.ptr().hashes[it.nodeIndex[it.depth]])
	*value = kvVal.toValue()

	it.nodeIndex[it.depth]++
	for node.hasChildren() {
		node = node.child(it.nodeIndex[it.depth])
		if it.depth++; it.depth == uint8(len(it.nodes)) {
			it.err = errors.New("max depth exceeded")
			return false
		}
		it.nodes[it.depth] = node
		it.nodeIndex[it.depth] = 0
	}
	for it.depth > 0 && it.nodeIndex[it.depth] == node.keyCount() {
		it.depth--
		node = it.nodes[it.depth]
	}
	return true
}

func (v Value) appendJSON(buf *bytes.Buffer) {
	switch v.Type {
	case TypeNull:
		buf.WriteString("null")
	case TypeBool:
		buf.WriteString(strconv.FormatBool(v.Bool()))
	case TypeInt:
		buf.WriteString(strconv.FormatInt(int64(v.Int()), 10))
	case TypeFloat:
		buf.WriteString(strconv.FormatFloat(v.Float64(), 'g', -1, 64))
	case TypeBytes:
		buf.WriteByte('"')
		buf.WriteString(base64.StdEncoding.EncodeToString(v.Bytes()))
		buf.WriteByte('"')
	case TypeString:
		buf.WriteString(strconv.Quote(v.String()))
	case TypeObject, TypeArray:
		v.c.appendJSON(buf)
	default:
		panic("unhandled type")
	}
}

func (c container) appendJSON(buf *bytes.Buffer) {
	if c.node.getType() == TypeArray {
		buf.WriteByte('[')
	} else {
		buf.WriteByte('{')
	}
	it := c.iter()
	first := true
	var key string
	var index int
	var value Value
	for it.next(&key, &index, &value) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		if key != "" {
			buf.WriteByte('"')
			buf.WriteString(key)
			buf.WriteByte('"')
			buf.WriteByte(':')
		}
		value.appendJSON(buf)
	}
	if c.node.getType() == TypeArray {
		buf.WriteByte(']')
	} else {
		buf.WriteByte('}')
	}
}

func (c container) unmarshalJSON(dec *json.Decoder) error {
	for i := uint32(0); ; i++ {
		t, err := dec.Token()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		} else if t == json.Delim('}') || t == json.Delim(']') {
			return nil
		}
		key := ""
		hash := i
		if c.node.getType() == TypeObject {
			key = t.(string)
			hash = keyHash(key)
			t, err = dec.Token()
			if err != nil {
				return err
			}
		}

		switch t := t.(type) {
		case json.Delim:
			if t == json.Delim('{') {
				child := c.setObject(key, uint32(hash))
				if err := child.c.unmarshalJSON(dec); err != nil {
					return err
				}
			} else {
				child := c.setArray(key, uint32(hash))
				if err := child.c.unmarshalJSON(dec); err != nil {
					return err
				}
			}
		case string:
			c.setValue(key, hash, String(t))
		case json.Number:
			if strings.ContainsAny(t.String(), ".eE") {
				f, err := t.Float64()
				if err != nil {
					return err
				}
				c.setValue(key, hash, Float64(f))
			} else {
				i, err := t.Int64()
				if err != nil {
					return err
				}
				c.setValue(key, hash, Uint64(uint64(i)))
			}
		case bool:
			c.setValue(key, hash, Bool(t))
		case nil:
			c.setValue(key, hash, Null())
		default:
			panic("unhandled JSON token type:" + fmt.Sprint(t))
		}
	}
}

func (b *Buffer) setRootContainer(typ uint8) container {
	b.buf = b.buf[:0]
	root := b.allocNode()
	root.setType(typ)
	return container{root}
}

func (b *Buffer) rootContainer() container {
	return container{b.treeNode(0)}
}

// Root returns the root container of the Buffer, which will be either an Object
// or an Array. If the Buffer is empty or has an invalid root, Root returns nil.
func (b *Buffer) Root() any {
	c := b.rootContainer()
	switch c.node.getType() {
	case TypeObject:
		return Object{c}
	case TypeArray:
		return Array{c}
	default:
		return nil
	}
}

// SetRootObject sets the root container of the Buffer to an empty Object and
// returns it.
func (b *Buffer) SetRootObject() Object { return Object{b.setRootContainer(TypeObject)} }

// SetRootArray sets the root container of the Buffer to an empty Array and
// returns it.
func (b *Buffer) SetRootArray() Array { return Array{b.setRootContainer(TypeArray)} }

// Bytes returns the underlying byte slice of the Buffer.
func (b *Buffer) Bytes() []byte { return b.buf }

// MarshalJSON implements json.Marshaler.
func (b *Buffer) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	b.rootContainer().appendJSON(&buf)
	return buf.Bytes(), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *Buffer) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	t, err := dec.Token()
	if err == io.EOF {
		return nil
	} else if t == json.Delim('[') {
		return b.SetRootArray().c.unmarshalJSON(dec)
	} else if t == json.Delim('{') {
		return b.SetRootObject().c.unmarshalJSON(dec)
	} else {
		return errors.New("invalid JSON root")
	}
}

// New creates a new Buffer backed by the given byte slice. If the Buffer later
// grows beyond the capacity of buf, a new underlying slice will be allocated.
func New(buf []byte) *Buffer {
	return &Buffer{buf: buf}
}
