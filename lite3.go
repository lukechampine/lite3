// Package lite3 is an implementation of the Lite³ serialization format.
package lite3

import (
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"unsafe"
)

const (
	maxKeyCount = 7
	minKeyCount = 3

	nodeSize = 96
	maxDepth = 9

	maxProbes = 128
)

func keyTagSize(key string) int {
	switch {
	case len(key) < 1<<6:
		return 1
	case len(key) < 1<<14:
		return 2
	default:
		return 3
	}
}

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
	TypeObject:  nodeSize - 1, // type byte hidden inside treeNode.genType
	TypeArray:   nodeSize - 1,
	TypeInvalid: 0,
}

type Buffer struct {
	buf []byte
}

func (b *Buffer) setRootContainer(typ uint8) container {
	buf := make([]byte, nodeSize)
	buf[0] = typ
	b.buf = append(b.buf[:0], buf...)
	return container{
		b:   b,
		typ: typ,
		off: 0,
	}
}

func (b *Buffer) SetRootObject() Object { return Object{b.setRootContainer(TypeObject)} }
func (b *Buffer) SetRootArray() Array   { return Array{b.setRootContainer(TypeArray)} }

func (b *Buffer) Bytes() []byte { return b.buf }

func New(buf []byte) *Buffer {
	return &Buffer{buf: buf}
}

type Object struct {
	c container
}

func (o Object) Count() int                         { return o.c.count() }
func (o Object) Iter() *Iter                        { return o.c.iter() }
func (o Object) SetNull(key string)                 { o.c.setNull(key, keyHash(key)) }
func (o Object) SetBool(key string, val bool)       { o.c.setBool(key, keyHash(key), val) }
func (o Object) Bool(key string) bool               { return o.c.bool(key, keyHash(key)) }
func (o Object) SetInt(key string, val int)         { o.c.setInt(key, keyHash(key), val) }
func (o Object) Int(key string) int                 { return o.c.int(key, keyHash(key)) }
func (o Object) SetFloat64(key string, val float64) { o.c.setFloat64(key, keyHash(key), val) }
func (o Object) Float64(key string) float64         { return o.c.float64(key, keyHash(key)) }
func (o Object) SetBytes(key string, val []byte)    { o.c.setBytes(key, keyHash(key), val) }
func (o Object) Bytes(key string) []byte            { return o.c.bytes(key, keyHash(key)) }
func (o Object) RawBytes(key string) []byte         { return o.c.rawBytes(key, keyHash(key)) }
func (o Object) SetString(key string, val string)   { o.c.setString(key, keyHash(key), val) }
func (o Object) String(key string) string           { return o.c.string(key, keyHash(key)) }
func (o Object) RawString(key string) string        { return o.c.rawString(key, keyHash(key)) }
func (o Object) SetObject(key string) Object        { return o.c.setObject(key, keyHash(key)) }
func (o Object) Object(key string) Object           { return o.c.object(key, keyHash(key)) }
func (o Object) SetArray(key string) Array          { return o.c.setArray(key, keyHash(key)) }
func (o Object) Array(key string) Array             { return o.c.array(key, keyHash(key)) }

type Array struct {
	c container
}

func (a Array) Count() int                    { return a.c.count() }
func (a Array) Iter() *Iter                   { return a.c.iter() }
func (a Array) SetNull(i int)                 { a.c.setNull("", uint32(i)) }
func (a Array) SetBool(i int, val bool)       { a.c.setBool("", uint32(i), val) }
func (a Array) Bool(i int) bool               { return a.c.bool("", uint32(i)) }
func (a Array) SetInt(i int, val int)         { a.c.setInt("", uint32(i), val) }
func (a Array) Int(i int) int                 { return a.c.int("", uint32(i)) }
func (a Array) SetFloat64(i int, val float64) { a.c.setFloat64("", uint32(i), val) }
func (a Array) Float64(i int) float64         { return a.c.float64("", uint32(i)) }
func (a Array) SetBytes(i int, val []byte)    { a.c.setBytes("", uint32(i), val) }
func (a Array) Bytes(i int) []byte            { return a.c.bytes("", uint32(i)) }
func (a Array) RawBytes(i int) []byte         { return a.c.rawBytes("", uint32(i)) }
func (a Array) SetString(i int, val string)   { a.c.setString("", uint32(i), val) }
func (a Array) String(i int) string           { return a.c.string("", uint32(i)) }
func (a Array) RawString(i int) string        { return a.c.rawString("", uint32(i)) }
func (a Array) SetObject(i int) Object        { return a.c.setObject("", uint32(i)) }
func (a Array) Object(i int) Object           { return a.c.object("", uint32(i)) }
func (a Array) SetArray(i int) Array          { return a.c.setArray("", uint32(i)) }
func (a Array) Array(i int) Array             { return a.c.array("", uint32(i)) }

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

func (b *Buffer) treeNode(off uint32) treeNode {
	u32s := (*[24]uint32)(unsafe.Pointer(&b.buf[off]))
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

func (node *treeNode) hasChildren() bool {
	return node.childOff[0] != 0
}

func (node *treeNode) flush(b *Buffer) {
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

func keyHash(key string) uint32 {
	// DJB2
	hash := uint32(5381)
	for i := range key {
		hash = ((hash << 5) + hash) + uint32(key[i])
	}
	return hash
}

func unsafeString(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }
func unsafeSlice(s string) []byte  { return unsafe.Slice(unsafe.StringData(s), len(s)) }

type container struct {
	b   *Buffer
	typ uint8
	off uint32
}

func (c *container) count() int  { return int(c.b.treeNode(c.off).size) }
func (c *container) iter() *Iter { return newIter(c.b, c.b.treeNode(c.off)) }

func (c *container) setContainer(key string, keyHash uint32, typ uint8) container {
	return container{
		b:   c.b,
		typ: typ,
		off: c.b.set(c.b.treeNode(c.off), key, keyHash, TypeObject, make([]byte, nodeSize-1)),
	}
}

func (c *container) container(key string, keyHash uint32, exp uint8) container {
	kv := c.b.get(c.b.treeNode(c.off), key, keyHash)
	if *kv.typ != exp {
		panic("type mismatch")
	}
	return container{
		b:   c.b,
		typ: *kv.typ,
		off: kv.typOff,
	}
}

func (c *container) setNull(key string, keyHash uint32) {
	c.b.set(c.b.treeNode(c.off), key, keyHash, TypeNull, nil)
}

func (c *container) setBool(key string, keyHash uint32, val bool) {
	var v byte
	if val {
		v = 1
	}
	c.b.set(c.b.treeNode(c.off), key, keyHash, TypeBool, []byte{v})
}

func (c *container) bool(key string, keyHash uint32) bool {
	kv := c.b.get(c.b.treeNode(c.off), key, keyHash)
	return kv.val[0] != 0
}

func (c *container) setNumber(key string, keyHash uint32, typ uint8, val uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], val)
	c.b.set(c.b.treeNode(c.off), key, keyHash, typ, buf[:])
}

func (c *container) number(key string, keyHash uint32, exp uint8) uint64 {
	kv := c.b.get(c.b.treeNode(c.off), key, keyHash)
	return binary.LittleEndian.Uint64(kv.val)
}

func (c *container) setInt(key string, keyHash uint32, val int) {
	c.setNumber(key, keyHash, TypeInt, uint64(val))
}

func (c *container) int(key string, keyHash uint32) int {
	return int(c.number(key, keyHash, TypeInt))
}

func (c *container) setFloat64(key string, keyHash uint32, val float64) {
	c.setNumber(key, keyHash, TypeFloat, math.Float64bits(val))
}

func (c *container) float64(key string, keyHash uint32) float64 {
	return math.Float64frombits(c.number(key, keyHash, TypeFloat))
}

func (c *container) setTypedBytes(key string, keyHash uint32, typ uint8, val []byte) {
	c.b.set(c.b.treeNode(c.off), key, keyHash, typ, val)
}

func (c *container) typedBytes(key string, keyHash uint32, exp uint8) []byte {
	kv := c.b.get(c.b.treeNode(c.off), key, keyHash)
	return kv.val
}

func (c *container) setBytes(key string, keyHash uint32, val []byte) {
	c.setTypedBytes(key, keyHash, TypeBytes, val)
}

func (c *container) bytes(key string, keyHash uint32) []byte {
	return slices.Clone(c.rawBytes(key, keyHash))
}

func (c *container) rawBytes(key string, keyHash uint32) []byte {
	return c.typedBytes(key, keyHash, TypeBytes)
}

func (c *container) setString(key string, keyHash uint32, val string) {
	c.setTypedBytes(key, keyHash, TypeString, unsafeSlice(val))
}

func (c *container) string(key string, keyHash uint32) string {
	return string(c.typedBytes(key, keyHash, TypeString))
}

func (c *container) rawString(key string, keyHash uint32) string {
	return unsafeString(c.typedBytes(key, keyHash, TypeString))
}

func (c *container) setObject(key string, keyHash uint32) Object {
	return Object{c.setContainer(key, keyHash, TypeObject)}
}

func (c *container) object(key string, keyHash uint32) Object {
	return Object{c.container(key, keyHash, TypeObject)}
}

func (c *container) setArray(key string, keyHash uint32) Array {
	return Array{c.setContainer(key, keyHash, TypeArray)}
}

func (c *container) array(key string, keyHash uint32) Array {
	return Array{c.container(key, keyHash, TypeArray)}
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

// appendEntry appends a fresh key/value payload to b.buf and returns the
// offset of the type byte. The caller is responsible for storing the kv offset.
func (b *Buffer) appendEntry(key string, valType uint8, val []byte) (uint32, bool) {
	if key != "" {
		tagSize := keyTagSize(key)
		b.buf = binary.LittleEndian.AppendUint32(b.buf, uint32(((len(key)+1)<<2)|(tagSize-1)))
		b.buf = b.buf[:len(b.buf)-4+tagSize]
		b.buf = append(b.buf, []byte(key)...) // TODO: copy instead
		b.buf = append(b.buf, 0)              // C string terminator
	}
	typOff := uint32(len(b.buf))
	b.buf = appendVal(b.buf, valType, val)
	return typOff, true
}

func (b *Buffer) insert(node treeNode, index uint8, key string, valType uint8) {
	alignmentMask := 0
	if valType == TypeObject || valType == TypeArray {
		alignmentMask = 3
	}
	unalignedValOffs := len(b.buf) + keyTagSize(key) + len(key)
	padding := ((unalignedValOffs + alignmentMask) & ^alignmentMask) - unalignedValOffs
	b.buf = b.buf[:len(b.buf)+padding]
	node.kvOff[index] = uint32(len(b.buf))
	node.flush(b)
}

type keyVal struct {
	key    []byte
	typ    *uint8 // &b.buf[typOff]
	val    []byte // if TypeString/TypeBytes, excludes length prefix and NULL terminator
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

// reuseSlot attempts to reuse an existing kv slot for a matching key.
//
// B-trees here store only hashes in nodes; the actual key bytes live in the
// value buffer. If two different keys share a hash, we detect the mismatch
// here and return ok=false so the caller can probe a different hash.
func (b *Buffer) reuseSlot(node treeNode, index uint8, key string, valType uint8, val []byte) (uint32, bool) {
	kv := b.readKV(node.kvOff[index], node.typ == TypeObject)
	if key != "" && string(kv.key) != key {
		return 0, false
	}
	clear(kv.val)
	if len(kv.val) < len(val) {
		// Existing value is too small. Allocate a new payload at the end of
		// the buffer, and clear the old bytes to avoid stale data.
		b.insert(node, index, key, valType)
		return b.appendEntry(key, valType, val)
	}
	// Existing value is large enough; reuse it and clear the unused tail so
	// reads don't see leftover bytes.
	*kv.typ = valType
	appendVal(b.buf[:kv.typOff], valType, val)
	return kv.typOff, true
}

// insertIntoLeaf inserts a new hash slot in sorted order and allocates space
// for its value in the buffer.
//
// A tree node stores parallel arrays of hashes and kv offsets. We shift the
// arrays right to make room at index, then write the new hash and kv offset.
func (b *Buffer) insertIntoLeaf(node treeNode, index uint8, hash uint32, key string, valType uint8) {
	copy(node.hashes[index+1:], node.hashes[index:])
	copy(node.kvOff[index+1:], node.kvOff[index:])

	node.hashes[index] = hash
	node.keyCount++
	b.insert(node, index, key, valType)
}

type insertCursor struct {
	node        treeNode
	parent      treeNode
	hasParent   bool
	parentIndex uint8
}

// splitFullNode splits a full node around the median and promotes the median
// hash into the parent. If there is no parent, it creates a new root.
//
// This is standard B-tree insertion: nodes hold sorted hashes and up to
// maxKeyCount entries. When a node is full, we:
//   - create a new sibling for the upper half,
//   - move the median hash up into the parent,
//   - keep the lower half in the original node.
//
// The cursor is updated to point at the correct child after the split.
// The return value reports whether attemptHash was promoted to the parent.
func (b *Buffer) splitFullNode(root treeNode, cursor *insertCursor, attemptHash uint32) bool {
	b.reserveSplitSpace(cursor.hasParent)
	if !cursor.hasParent {
		// Splitting the root: turn the old root into a child and reset the
		// new root to have a single child pointer.
		b.splitRoot(root, cursor)
	}

	// Allocate the new sibling node that will hold the upper half.
	sibling := b.allocSibling(cursor.node)
	sepHash := b.promoteMedian(&cursor.parent, cursor.node, sibling.off, cursor.parentIndex)
	b.splitNode(&cursor.node, &sibling)

	// Determine which node the caller should continue searching in.
	if attemptHash == sepHash {
		// The median moved up; the caller must operate on the parent.
		return true
	}
	if attemptHash > sepHash {
		// The promoted median now lives in the parent at parentIndex.
		cursor.node = sibling
		cursor.parentIndex++
	}
	return false
}

func (b *Buffer) reserveSplitSpace(hasParent bool) {
	buflenAligned := (len(b.buf) + 3) & ^3
	newNodeSize := nodeSize
	if hasParent {
		newNodeSize = 2 * nodeSize
	}
	if len(b.buf)+newNodeSize+buflenAligned > cap(b.buf) {
		b.buf = append(b.buf, make([]byte, newNodeSize+buflenAligned)...)
	}
	b.buf = b.buf[:buflenAligned]
}

func (b *Buffer) splitRoot(root treeNode, cursor *insertCursor) {
	cursor.node.off = uint32(len(b.buf))
	b.buf = b.buf[:len(b.buf)+nodeSize]
	cursor.node.flush(b)
	cursor.parent = treeNode{
		off:      root.off,
		typ:      root.typ,
		gen:      root.gen,
		size:     root.size,
		childOff: [8]uint32{0: cursor.node.off},
	}
	cursor.hasParent = true
	cursor.parentIndex = 0
}

func (b *Buffer) allocSibling(node treeNode) treeNode {
	siblingOff := uint32(len(b.buf))
	b.buf = b.buf[:len(b.buf)+nodeSize]
	return treeNode{
		off:      siblingOff,
		typ:      node.typ,
		keyCount: minKeyCount,
	}
}

func (b *Buffer) promoteMedian(parent *treeNode, node treeNode, siblingOff uint32, i uint8) uint32 {
	// Make space in the parent for the promoted median hash.
	copy(parent.hashes[i+1:], parent.hashes[i:])
	parent.hashes[i] = node.hashes[minKeyCount]
	copy(parent.kvOff[i+1:], parent.kvOff[i:])
	parent.kvOff[i] = node.kvOff[minKeyCount]
	copy(parent.childOff[i+1:], parent.childOff[i:])
	parent.childOff[i+1] = siblingOff

	parent.keyCount++
	parent.flush(b)
	return parent.hashes[i]
}

func (b *Buffer) splitNode(node *treeNode, sibling *treeNode) {
	// Clear the promoted slot and move upper-half keys/children to the sibling.
	node.hashes[minKeyCount] = 0
	node.kvOff[minKeyCount] = 0

	copy(sibling.hashes[:minKeyCount], node.hashes[minKeyCount+1:])
	copy(sibling.kvOff[:minKeyCount], node.kvOff[minKeyCount+1:])
	copy(sibling.childOff[:minKeyCount+1], node.childOff[minKeyCount+1:])
	clear(node.hashes[minKeyCount+1:])
	clear(node.kvOff[minKeyCount+1:])
	clear(node.childOff[minKeyCount+1:])
	node.size = 0
	node.keyCount = minKeyCount
	node.flush(b)
	sibling.flush(b)
}

// findSlot returns the index where hash should appear and whether it exists.
// Nodes are small, so a simple linear scan is fine here.
func findSlot(node treeNode, hash uint32) (uint8, bool) {
	var i uint8
	for i < node.keyCount && node.hashes[i] < hash {
		i++
	}
	return i, i < node.keyCount && node.hashes[i] == hash
}

// trySet walks the hash B-tree and either reuses a matching slot or inserts a
// new one. A false ok indicates a hash collision with a different key.
//
// The tree is indexed by hash (not full key). This function is called with a
// candidate hash; the caller will retry with a different probe hash if we
// discover a collision.
func (b *Buffer) trySet(root treeNode, key string, attemptHash uint32, valType uint8, val []byte) (uint32, bool) {
	cursor := insertCursor{node: root}
	for range maxDepth {
		if cursor.node.keyCount == maxKeyCount {
			// Split on the way down so we never descend into a full node.
			// This is the standard B-tree insertion strategy.
			if b.splitFullNode(root, &cursor, attemptHash) {
				// The hash we're looking for was promoted to the parent during
				// the split. Operate on the parent directly instead of descending.
				return b.reuseSlot(cursor.parent, cursor.parentIndex, key, valType, val)
			}
		}

		i, exact := findSlot(cursor.node, attemptHash)
		if exact {
			// Hash exists in this node; confirm key match and reuse if possible.
			return b.reuseSlot(cursor.node, i, key, valType, val)
		}

		if !cursor.node.hasChildren() {
			// Leaf insert: create a new slot and append the value payload.
			b.insertIntoLeaf(cursor.node, i, attemptHash, key, valType)
			root = b.treeNode(root.off)
			// root.size tracks the total number of entries for iteration.
			root.size++
			root.flush(b)
			return b.appendEntry(key, valType, val)
		}

		// Internal node: descend into the child whose range would contain
		// attemptHash, keeping parent info for possible future splits.
		cursor.parent = cursor.node
		cursor.hasParent = true
		cursor.parentIndex = i
		cursor.node = b.treeNode(cursor.node.childOff[i])
	}
	panic("node walks exceeded maxDepth")
}

func (b *Buffer) set(root treeNode, key string, keyHash uint32, valType uint8, val []byte) uint32 {
	root.gen++
	root.flush(b)
	for probe := range uint32(maxProbes) {
		if typOff, ok := b.trySet(root, key, keyHash+(probe*probe), valType, val); ok {
			return typOff
		}
	}
	panic("maxProbes limit reached")
}

func (b *Buffer) get(root treeNode, key string, keyHash uint32) keyVal {
outer:
	for probe := range uint32(maxProbes) {
		probeHash := keyHash + probe*probe

		node := root
		for range maxDepth {
			i, exact := findSlot(node, probeHash)
			if exact {
				kv := b.readKV(node.kvOff[i], key != "")
				if key != "" && string(kv.key) != key {
					continue outer // try next probe
				}
				return kv
			}
			if !node.hasChildren() {
				panic("key not found: " + key)
			}
			node = b.treeNode(node.childOff[i])
		}
		panic("node walks exceeded maxDepth")
	}
	panic("maxProbes limit reached")
}

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
	default:
		panic("unhandled type")
	}
}

func (v Value) Bool() bool        { return v.data[0] != 0 }
func (v Value) Uint64() uint64    { return binary.LittleEndian.Uint64(v.data) }
func (v Value) Int() int          { return int(v.Uint64()) }
func (v Value) Float64() float64  { return math.Float64frombits(v.Uint64()) }
func (v Value) RawBytes() []byte  { return v.data }
func (v Value) Bytes() []byte     { return slices.Clone(v.RawBytes()) }
func (v Value) String() string    { return string(v.RawBytes()) }
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
	nodeOffs  [maxDepth + 1]uint32
	depth     uint8
	nodeIndex [maxDepth + 1]uint8
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
		if it.depth++; it.depth > maxDepth {
			return &Iter{err: errors.New("node walks exceeded maxDepth")}
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
		if it.depth++; it.depth > maxDepth {
			it.err = errors.New("node walks exceeded maxDepth")
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
