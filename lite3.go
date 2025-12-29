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
		// Locate the node that should store h.
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
				kvOff, typOff := c.b.appendKV(key, valType, val)
				node.insertKV(i, h, kvOff)
				c.b.flushNode(&node)
				// increment container count
				root = c.b.treeNode(root.off)
				root.size++
				c.b.flushNode(&root)
				return typOff, true
			} else {
				if child := c.b.treeNode(node.childOff[i]); child.isFull() {
					// we'll descend into the correct child on the next iteration
					c.b.splitChild(&node, i)
				} else {
					node = child
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

func (c container) count() int  { return int(c.b.treeNode(c.off).size) }
func (c container) iter() *Iter { return newIter(c.b, c.b.treeNode(c.off)) }

func (c container) value(key string, keyHash uint32) Value {
	kv := c.get(key, keyHash)
	if kv.typ == nil {
		return Value{Type: TypeInvalid}
	}
	return Value{
		Type: *kv.typ,
		data: kv.val,
		c:    container{c.b, *kv.typ, kv.typOff},
	}
}

func (c container) setContainer(key string, keyHash uint32, typ uint8) container {
	return container{
		b:   c.b,
		typ: typ,
		off: c.set(key, keyHash, typ, make([]byte, nodeSize-1)),
	}
}

func (c container) setNull(key string, keyHash uint32) {
	c.set(key, keyHash, TypeNull, nil)
}

func (c container) setBool(key string, keyHash uint32, val bool) {
	var v [1]byte
	if val {
		v[0] = 1
	}
	c.set(key, keyHash, TypeBool, v[:])
}

func (c container) setNumber(key string, keyHash uint32, typ uint8, val uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], val)
	c.set(key, keyHash, typ, buf[:])
}

func (c container) setInt(key string, keyHash uint32, val int) {
	c.setNumber(key, keyHash, TypeInt, uint64(val))
}

func (c container) setFloat64(key string, keyHash uint32, val float64) {
	c.setNumber(key, keyHash, TypeFloat, math.Float64bits(val))
}

func (c container) setTypedBytes(key string, keyHash uint32, typ uint8, val []byte) {
	c.set(key, keyHash, typ, val)
}

func (c container) setBytes(key string, keyHash uint32, val []byte) {
	c.setTypedBytes(key, keyHash, TypeBytes, val)
}

func (c container) setString(key string, keyHash uint32, val string) {
	c.setTypedBytes(key, keyHash, TypeString, unsafeSlice(val))
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

func (o Object) NumFields() int                     { return o.c.count() }
func (o Object) Iter() *Iter                        { return o.c.iter() }
func (o Object) Value(key string) Value             { return o.c.value(key, keyHash(key)) }
func (o Object) SetNull(key string)                 { o.c.setNull(key, keyHash(key)) }
func (o Object) SetBool(key string, val bool)       { o.c.setBool(key, keyHash(key), val) }
func (o Object) SetInt(key string, val int)         { o.c.setInt(key, keyHash(key), val) }
func (o Object) SetFloat64(key string, val float64) { o.c.setFloat64(key, keyHash(key), val) }
func (o Object) SetBytes(key string, val []byte)    { o.c.setBytes(key, keyHash(key), val) }
func (o Object) SetString(key string, val string)   { o.c.setString(key, keyHash(key), val) }
func (o Object) SetObject(key string) Object        { return o.c.setObject(key, keyHash(key)) }
func (o Object) SetArray(key string) Array          { return o.c.setArray(key, keyHash(key)) }
func (o Object) All() iter.Seq2[string, Value] {
	return func(yield func(string, Value) bool) {
		it := o.Iter()
		for elem := it.Next(); elem != nil; elem = it.Next() {
			if !yield(elem.Key, elem.Value) {
				return
			}
		}
	}
}

type Array struct {
	c container
}

func (a Array) Len() int                      { return a.c.count() }
func (a Array) Iter() *Iter                   { return a.c.iter() }
func (a Array) Value(i int) Value             { return a.c.value("", uint32(i)) }
func (a Array) SetNull(i int)                 { a.c.setNull("", uint32(i)) }
func (a Array) SetBool(i int, val bool)       { a.c.setBool("", uint32(i), val) }
func (a Array) SetInt(i int, val int)         { a.c.setInt("", uint32(i), val) }
func (a Array) SetFloat64(i int, val float64) { a.c.setFloat64("", uint32(i), val) }
func (a Array) SetBytes(i int, val []byte)    { a.c.setBytes("", uint32(i), val) }
func (a Array) SetString(i int, val string)   { a.c.setString("", uint32(i), val) }
func (a Array) SetObject(i int) Object        { return a.c.setObject("", uint32(i)) }
func (a Array) SetArray(i int) Array          { return a.c.setArray("", uint32(i)) }

type Value struct {
	Type uint8
	data []byte
	c    container
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

func (v Value) Bool() bool       { return v.data[0] != 0 }
func (v Value) Uint64() uint64   { return binary.LittleEndian.Uint64(v.data) }
func (v Value) Int() int         { return int(v.Uint64()) }
func (v Value) Float64() float64 { return math.Float64frombits(v.Uint64()) }
func (v Value) RawBytes() []byte { return v.data }
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

type IterElem struct {
	Key   string
	Index int
	Value Value
}

type Iter struct {
	b         *Buffer
	gen       uint32
	nodeOffs  [10]uint32
	depth     uint8
	nodeIndex [10]uint8
	err       error
}

func newIter(b *Buffer, node treeNode) *Iter {
	it := &Iter{
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

func (it *Iter) Err() error {
	return it.err
}

func (it *Iter) Next() *IterElem {
	if it.err != nil {
		return nil
	} else if it.gen != it.b.treeNode(it.nodeOffs[0]).gen {
		return nil
	}
	node := it.b.treeNode(it.nodeOffs[it.depth])
	if !(node.typ == TypeObject || node.typ == TypeArray) {
		it.err = errors.New("not an array or object type")
		return nil
	} else if it.depth == 0 && it.nodeIndex[it.depth] == node.keyCount {
		return nil
	}

	kv := it.b.readKV(node.kvOff[it.nodeIndex[it.depth]], node.typ == TypeObject)
	index := int(node.hashes[it.nodeIndex[it.depth]])
	it.nodeIndex[it.depth]++

	for node.hasChildren() {
		node = it.b.treeNode(node.childOff[it.nodeIndex[it.depth]])
		if it.depth++; it.depth == uint8(len(it.nodeOffs)) {
			it.err = errors.New("max depth exceeded")
			return nil
		}
		it.nodeOffs[it.depth] = node.off
		it.nodeIndex[it.depth] = 0
	}
	for it.depth > 0 && it.nodeIndex[it.depth] == node.keyCount {
		it.depth--
		node = it.b.treeNode(it.nodeOffs[it.depth])
	}
	return &IterElem{
		Key:   unsafeString(kv.key),
		Index: index,
		Value: Value{
			Type: *kv.typ,
			data: kv.val,
			c:    container{it.b, *kv.typ, kv.typOff},
		},
	}
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
	for elem := it.Next(); elem != nil; elem = it.Next() {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		if elem.Key != "" {
			buf.WriteByte('"')
			buf.WriteString(elem.Key)
			buf.WriteByte('"')
			buf.WriteByte(':')
		}
		elem.Value.appendJSON(buf)
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
			c.setString(key, hash, t)
		case json.Number:
			if strings.ContainsAny(t.String(), ".eE") {
				f, err := t.Float64()
				if err != nil {
					return err
				}
				c.setFloat64(key, hash, f)
			} else {
				i, err := t.Int64()
				if err != nil {
					return err
				}
				c.setNumber(key, hash, TypeInt, uint64(i))
			}
		case bool:
			c.setBool(key, hash, t)
		case nil:
			c.setNull(key, hash)
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
