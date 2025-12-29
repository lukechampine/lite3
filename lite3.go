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

type treeNode struct {
	off      uint32
	typ      uint8
	gen      uint32
	hashes   [7]uint32
	keyCount uint8
	size     uint32
	kvOff    [7]uint32
	childOff [8]uint32
}

func (b *Buffer) allocNode() (offset uint32) {
	b.grow(((len(b.buf) + 3) & ^3) - len(b.buf))
	b.grow(nodeSize)
	return uint32(len(b.buf) - nodeSize)
}

func (b *Buffer) treeNode(off uint32) treeNode {
	var u32s [24]uint32
	for i := range u32s {
		u32s[i] = binary.LittleEndian.Uint32(b.buf[off+uint32(i*4):])
	}
	return treeNode{
		off:      off,
		typ:      uint8(u32s[0]),
		gen:      u32s[0] >> 8,
		hashes:   [7]uint32(u32s[1:8]),
		keyCount: uint8(u32s[8] & 7),
		size:     u32s[8] >> 6,
		kvOff:    [7]uint32(u32s[9:16]),
		childOff: [8]uint32(u32s[16:24]),
	}
}

func (b *Buffer) flushNode(node *treeNode) {
	genType := (uint32(node.typ) & 0xFF) | (node.gen << 8)
	sizeKC := (node.size << 6) | (uint32(node.keyCount) & 7)

	buf := b.buf[:node.off]
	buf = binary.LittleEndian.AppendUint32(buf, genType)
	for _, u := range &node.hashes {
		buf = binary.LittleEndian.AppendUint32(buf, u)
	}
	buf = binary.LittleEndian.AppendUint32(buf, sizeKC)
	for _, u := range &node.kvOff {
		buf = binary.LittleEndian.AppendUint32(buf, u)
	}
	for _, u := range &node.childOff {
		buf = binary.LittleEndian.AppendUint32(buf, u)
	}
}

func (node *treeNode) isFull() bool {
	return len(node.hashes[node.keyCount:]) == 0
}

func (node *treeNode) hasChildren() bool {
	// The "correct" check is node.childOff == ([8]uint8{}), but since:
	//   a) children are stored in sorted order, and
	//   b) the only node with an offset of 0 is the buffer root, and
	//   c) that node cannot be a child of any other node,
	// this is equivalent.
	return node.childOff[0] != 0
}

func (node *treeNode) slot(hash uint32) (i uint8, exists bool) {
	for i < node.keyCount && node.hashes[i] < hash {
		i++
	}
	return i, i < node.keyCount && node.hashes[i] == hash
}

func (node *treeNode) insertKV(i uint8, hash uint32, off uint32) {
	copy(node.hashes[i+1:], node.hashes[i:node.keyCount])
	copy(node.kvOff[i+1:], node.kvOff[i:node.keyCount])
	node.hashes[i] = hash
	node.kvOff[i] = off
	node.keyCount++
}

func (node *treeNode) insertChild(i uint8, off uint32) {
	copy(node.childOff[i+1:], node.childOff[i:])
	node.childOff[i] = off
}

type keyVal struct {
	key    []byte
	typ    *uint8 // &b.buf[typOff]
	val    []byte // if TypeString/TypeBytes, excludes length prefix and C string terminator
	typOff uint32
}

func (b *Buffer) readKV(off uint32, hasKey bool) (kv keyVal) {
	buf := b.buf[off:]
	if hasKey {
		tagSize := uint32(buf[0]&3) + 1
		keySize := binary.LittleEndian.Uint32(buf) & ((1 << 8 * tagSize) - 1) >> 2
		kv.key = buf[tagSize:][:keySize-1] // exclude C string terminator
		kv.typOff = tagSize + keySize
		buf = buf[tagSize+keySize:]
	}
	kv.typOff += off
	kv.typ = &buf[0]
	kv.val = buf[1:][:typeSizes[*kv.typ]]
	if *kv.typ == TypeString || *kv.typ == TypeBytes {
		kv.val = kv.val[4:][:binary.LittleEndian.Uint32(kv.val)-1] // exclude C string terminator
	}
	return
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

type container struct {
	b   *Buffer
	typ uint8
	off uint32
}

func (c container) get(key string, keyHash uint32) keyVal {
	try := func(h uint32) (keyVal, bool) {
		node := c.b.treeNode(c.off)
		for {
			i, exists := node.slot(h)
			if exists {
				kv := c.b.readKV(node.kvOff[i], key != "")
				if key != "" && string(kv.key) != key {
					return keyVal{}, false
				}
				return kv, true
			} else if !node.hasChildren() {
				return keyVal{}, true // not found
			}
			node = c.b.treeNode(node.childOff[i])
		}
	}

	for probe := uint32(0); ; probe++ {
		if kv, ok := try(keyHash + probe*probe); ok {
			return kv
		}
	}
}

func (b *Buffer) splitChild(node *treeNode, i uint8) {
	// Move median into parent
	child := b.treeNode(node.childOff[i])
	const med = uint8(len(child.hashes) / 2)
	node.insertKV(i, child.hashes[med], child.kvOff[med])

	// Move lower half of entries into new sibling
	sibling := treeNode{
		off:      b.allocNode(),
		typ:      node.typ,
		keyCount: med,
	}
	copy(sibling.hashes[:med], child.hashes[med+1:])
	copy(sibling.kvOff[:med], child.kvOff[med+1:])
	copy(sibling.childOff[:med], child.childOff[med+1:])
	clear(child.hashes[med:])
	clear(child.kvOff[med:])
	clear(child.childOff[med:])
	child.keyCount = med
	child.size = 0

	// record sibling in parent
	node.insertChild(i+1, sibling.off)

	b.flushNode(node)
	b.flushNode(&child)
	b.flushNode(&sibling)
}

func (c container) set(key string, keyHash uint32, valType uint8, val []byte) uint32 {
	root := c.b.treeNode(c.off)
	root.gen++
	c.b.flushNode(&root)

	// Special case: root is full, and we don't have a parent to split into;
	// create a new parent to point to the current root.
	if root.isFull() {
		parent := treeNode{
			off:  root.off,
			typ:  root.typ,
			gen:  root.gen,
			size: root.size,
		}
		root.off = c.b.allocNode()
		c.b.flushNode(&root)
		parent.insertChild(0, root.off)
		c.b.splitChild(&parent, 0)
		root = parent
	}

	try := func(h uint32) (uint32, bool) {
		node := root
		for {
			if i, exists := node.slot(h); exists {
				cur := c.b.readKV(node.kvOff[i], node.typ == TypeObject)
				if string(cur.key) != key {
					return 0, false // collision
				} else if len(cur.val) >= len(val) {
					// Reuse existing KV space
					*cur.typ = valType
					clear(cur.val[len(val):]) // clear trailing garbage
					appendVal(c.b.buf[:cur.typOff], valType, val)
					return cur.typOff, true
				}
				// Append new KV and wipe the old one
				clear(cur.key)
				*cur.typ = 0
				clear(cur.val)
				kvOff, typOff := c.b.appendKV(key, valType, val)
				node.kvOff[i] = kvOff
				c.b.flushNode(&node)
				return typOff, true
			} else if !node.hasChildren() {
				// Leaf node with capacity; insert here
				kvOff, typOff := c.b.appendKV(key, valType, val)
				node.insertKV(i, h, kvOff)
				c.b.flushNode(&node)
				// increment container count
				root = c.b.treeNode(root.off)
				root.size++
				c.b.flushNode(&root)
				return typOff, true
			} else {
				// Descend into child (or split if full)
				if child := c.b.treeNode(node.childOff[i]); !child.isFull() {
					node = child
				} else {
					c.b.splitChild(&node, i) // we'll descend on next iteration
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

func (c container) count() int   { return int(c.b.treeNode(c.off).size) }
func (c container) iter() *liter { return newLiter(c.b, c.b.treeNode(c.off)) }

func (c container) value(key string, keyHash uint32) Value {
	return c.get(key, keyHash).toValue(c.b)
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
	return container{
		b:   c.b,
		typ: typ,
		off: c.set(key, keyHash, typ, make([]byte, nodeSize-1)),
	}
}

func (c container) setObject(key string, keyHash uint32) Object {
	return Object{c.setContainer(key, keyHash, TypeObject)}
}

func (c container) setArray(key string, keyHash uint32) Array {
	return Array{c.setContainer(key, keyHash, TypeArray)}
}

type Object struct {
	c container
}

func (o Object) NumFields() int              { return o.c.count() }
func (o Object) Get(key string) Value        { return o.c.value(key, keyHash(key)) }
func (o Object) Set(key string, val Value)   { o.c.setValue(key, keyHash(key), val) }
func (o Object) SetObject(key string) Object { return o.c.setObject(key, keyHash(key)) }
func (o Object) SetArray(key string) Array   { return o.c.setArray(key, keyHash(key)) }
func (o Object) All() iter.Seq2[string, Value] {
	return func(yield func(string, Value) bool) {
		it := o.c.iter()
		var key string
		var index int
		var value Value
		for it.Next(&key, &index, &value) {
			if !yield(key, value) {
				return
			}
		}
	}
}

type Array struct {
	c container
}

func (a Array) Len() int               { return a.c.count() }
func (a Array) Get(i int) Value        { return a.c.value("", uint32(i)) }
func (a Array) Set(i int, val Value)   { a.c.setValue("", uint32(i), val) }
func (a Array) SetObject(i int) Object { return a.c.setObject("", uint32(i)) }
func (a Array) SetArray(i int) Array   { return a.c.setArray("", uint32(i)) }
func (a Array) All() iter.Seq2[int, Value] {
	return func(yield func(int, Value) bool) {
		it := a.c.iter()
		var key string
		var index int
		var value Value
		for it.Next(&key, &index, &value) {
			if !yield(index, value) {
				return
			}
		}
	}
}

type Value struct {
	Type uint8
	num  uint64
	str  []byte
	c    container
}

func (kv keyVal) toValue(b *Buffer) Value {
	if kv.typ == nil || *kv.typ == TypeInvalid {
		return Value{Type: TypeInvalid}
	}
	switch *kv.typ {
	case TypeNull:
		return Value{Type: TypeNull}
	case TypeBool:
		return Value{
			Type: TypeBool,
			num:  uint64(kv.val[0]),
		}
	case TypeInt, TypeFloat:
		return Value{
			Type: *kv.typ,
			num:  binary.LittleEndian.Uint64(kv.val),
		}
	case TypeString, TypeBytes:
		return Value{
			Type: *kv.typ,
			str:  kv.val,
		}
	case TypeObject, TypeArray:
		return Value{
			Type: *kv.typ,
			c:    container{b: b, typ: *kv.typ, off: kv.typOff},
		}
	default:
		panic("unhandled type")
	}
}

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

func (v Value) Bool() bool       { return v.num != 0 }
func (v Value) Uint64() uint64   { return v.num }
func (v Value) Int() int         { return int(v.Uint64()) }
func (v Value) Float64() float64 { return math.Float64frombits(v.Uint64()) }
func (v Value) RawBytes() []byte { return v.str }
func (v Value) Bytes() []byte    { return slices.Clone(v.RawBytes()) }
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
func (v Value) RawString() string { return unsafeString(v.RawBytes()) }
func (v Value) Object() Object    { return Object{c: v.c} }
func (v Value) Array() Array      { return Array{c: v.c} }

func Null() Value { return Value{Type: TypeNull} }
func Bool(b bool) Value {
	if b {
		return Value{Type: TypeBool, num: 1}
	}
	return Value{Type: TypeBool, num: 0}
}
func Uint64(u uint64) Value   { return Value{Type: TypeInt, num: u} }
func Int(i int) Value         { return Uint64(uint64(i)) }
func Float64(f float64) Value { return Value{Type: TypeFloat, num: math.Float64bits(f)} }
func Bytes(b []byte) Value    { return Value{Type: TypeBytes, str: b} }
func String(s string) Value   { return Value{Type: TypeString, str: unsafeSlice(s)} }

type liter struct {
	b         *Buffer
	gen       uint32
	nodeOffs  [10]uint32
	depth     uint8
	nodeIndex [10]uint8
	err       error
}

func newLiter(b *Buffer, node treeNode) *liter {
	it := &liter{
		b:   b,
		gen: node.gen,
	}
	it.nodeOffs[0] = node.off

	for node.hasChildren() {
		node = b.treeNode(node.childOff[0])
		if it.depth++; it.depth == uint8(len(it.nodeOffs)) {
			it.err = errors.New("max depth exceeded")
			return nil
		}
		it.nodeOffs[it.depth] = node.off
		it.nodeIndex[it.depth] = 0
	}
	return it
}

func (it *liter) Next(key *string, index *int, value *Value) bool {
	if it.err != nil {
		return false
	} else if it.gen != it.b.treeNode(it.nodeOffs[0]).gen {
		return false
	}
	node := it.b.treeNode(it.nodeOffs[it.depth])
	if !(node.typ == TypeObject || node.typ == TypeArray) {
		it.err = errors.New("not an array or object type")
		return false
	} else if it.depth == 0 && it.nodeIndex[it.depth] == node.keyCount {
		return false
	}

	kv := it.b.readKV(node.kvOff[it.nodeIndex[it.depth]], node.typ == TypeObject)
	*key = unsafeString(kv.key)
	*index = int(node.hashes[it.nodeIndex[it.depth]])
	*value = kv.toValue(it.b)

	it.nodeIndex[it.depth]++
	for node.hasChildren() {
		node = it.b.treeNode(node.childOff[it.nodeIndex[it.depth]])
		if it.depth++; it.depth == uint8(len(it.nodeOffs)) {
			it.err = errors.New("max depth exceeded")
			return false
		}
		it.nodeOffs[it.depth] = node.off
		it.nodeIndex[it.depth] = 0
	}
	for it.depth > 0 && it.nodeIndex[it.depth] == node.keyCount {
		it.depth--
		node = it.b.treeNode(it.nodeOffs[it.depth])
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
	if c.typ == TypeArray {
		buf.WriteByte('[')
	} else {
		buf.WriteByte('{')
	}
	it := c.iter()
	first := true
	var key string
	var index int
	var value Value
	for it.Next(&key, &index, &value) {
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
	if c.typ == TypeArray {
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
		if c.typ == TypeObject {
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
	b.grow(nodeSize)
	n := treeNode{off: 0, typ: typ}
	b.flushNode(&n)
	return container{
		b:   b,
		typ: typ,
		off: 0,
	}
}

func (b *Buffer) rootContainer() container {
	return container{b: b, typ: b.buf[0], off: 0}
}

func (b *Buffer) Root() any {
	c := b.rootContainer()
	switch c.typ {
	case TypeObject:
		return Object{c}
	case TypeArray:
		return Array{c}
	default:
		return nil
	}
}

func (b *Buffer) SetRootObject() Object { return Object{b.setRootContainer(TypeObject)} }
func (b *Buffer) SetRootArray() Array   { return Array{b.setRootContainer(TypeArray)} }

func (b *Buffer) Bytes() []byte { return b.buf }

func (b *Buffer) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	b.rootContainer().appendJSON(&buf)
	return buf.Bytes(), nil
}

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

func New(buf []byte) *Buffer {
	return &Buffer{buf: buf}
}
