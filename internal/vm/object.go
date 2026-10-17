package vm

import (
	"unicode/utf8"
	"unsafe"
)

// ObjType represents the type of a heap-allocated object.
type ObjType byte

const (
	ObjTypeString ObjType = iota
	ObjTypeArray
	ObjTypeObject
	ObjTypeFunction       // Phase A: Basic functions (Phase B will add upvalues)
	ObjTypeExternFunction // External (host) functions
	ObjTypeUpvalue        // Phase B: For closure support
	ObjTypeIndex          // Where the keys of a large dictionary are (see native_dict.go)
)

// ObjHeader is the header for all heap-allocated objects.
type ObjHeader struct {
	Type   ObjType
	Marked bool
	// Frozen marks what a constant holds: an object or an array that is
	// made once and is the same wherever the constant is used, so that
	// nothing may change it (see freeze).
	Frozen bool
	Next   *ObjHeader
}

// Obj is the interface for all heap-allocated objects.
type Obj interface {
	Header() *ObjHeader
	String() string
}

// StringObj represents a heap-allocated string.
type StringObj struct {
	header ObjHeader
	data   string

	// Positions are counted in characters, the bytes of the text are
	// UTF-8. What makes one into the other is worked out the first time it
	// is asked for and kept: the number of characters, and for a text that
	// is not plain ASCII where every runeStride-th character starts. That
	// last part is rare and kept apart: a text is 48 bytes without it.
	runes    int32
	measured bool
	ascii    bool
	side     *stringSide
}

// stringSide is what few texts have, kept apart so that the others are
// small. A text made by joining two long ones is the two, until its bytes
// are asked for (see ropeMin): then they are written out once, and it is a
// text like any other. A text written out that is not plain ASCII has
// where every runeStride-th character starts.
type stringSide struct {
	starts      []uint32
	left, right *StringObj // The texts it is made of, not written out yet
	size        int        // How many bytes they make
	heap        *Heap      // Which counts the bytes once they are written out
}

// rope returns the texts a text is made of, if it is not written out yet.
func (s *StringObj) rope() *stringSide {
	if s.side != nil && s.side.left != nil {
		return s.side
	}
	return nil
}

// ropeMin is how long a text joined of two must be to be kept as the two:
// joining a part to a long text then copies nothing, and a text built a
// part at a time is written out once, not once per part.
const ropeMin = 1024

// runeStride is how far apart the recorded character positions are: the
// most that is decoded to reach any one character.
const runeStride = 32

func (s *StringObj) Header() *ObjHeader {
	return &s.header
}

func (s *StringObj) String() string {
	return s.Data()
}

// Data is the bytes of the text, written out first if it is still the
// texts it was joined of.
func (s *StringObj) Data() string {
	if s.side != nil && s.side.left != nil {
		s.flatten()
	}
	return s.data
}

// ByteLen is how many bytes the text has, written out or not.
func (s *StringObj) ByteLen() int {
	if rope := s.rope(); rope != nil {
		return rope.size
	}
	return len(s.data)
}

// flatten writes out the bytes of a text kept as the texts it was joined
// of, and lets go of them. A text built a part at a time is a long chain
// of joins: it is walked with a stack of its own, not by recursion.
func (s *StringObj) flatten() {
	buf := make([]byte, 0, s.side.size)
	pending := []*StringObj{s}
	for len(pending) > 0 {
		part := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if rope := part.rope(); rope != nil {
			pending = append(pending, rope.right, rope.left)
			continue
		}
		buf = append(buf, part.data...)
	}
	s.data = unsafe.String(unsafe.SliceData(buf), len(buf))
	// The text was counted without its bytes; it has them now. They are
	// counted whatever the limit: the next allocation collects, or fails
	s.side.heap.bytesAllocated += uint64(len(buf))
	s.side = nil
}

func (s *StringObj) measure() {
	if s.rope() != nil {
		s.flatten()
	}
	s.measured = true
	s.ascii = true
	for i := 0; i < len(s.data); i++ {
		if s.data[i] >= utf8.RuneSelf {
			s.ascii = false
			break
		}
	}
	if s.ascii {
		s.runes = int32(len(s.data))
		return
	}
	var starts []uint32
	n := 0
	for offset := range s.data {
		if n%runeStride == 0 {
			starts = append(starts, uint32(offset))
		}
		n++
	}
	s.runes, s.side = int32(n), &stringSide{starts: starts}
}

// RuneLen is the length of the text in characters.
func (s *StringObj) RuneLen() int {
	if !s.measured {
		s.measure()
	}
	return int(s.runes)
}

// offsetOf is where the character at a position starts among the bytes of
// the text. The position may be the length: the end of the text.
func (s *StringObj) offsetOf(i int) int {
	if s.rope() != nil {
		s.flatten()
	}
	// A text written out of parts knows how many characters it has, not
	// where they are
	if !s.measured || (!s.ascii && s.side == nil) {
		s.measure()
	}
	if s.ascii {
		return i
	}
	if i >= int(s.runes) {
		return len(s.data)
	}
	offset := int(s.side.starts[i/runeStride])
	for skip := i % runeStride; skip > 0; skip-- {
		_, size := utf8.DecodeRuneInString(s.data[offset:])
		offset += size
	}
	return offset
}

// RuneAt returns the character at a position, as the bytes of the text
// that hold it. The position must be within the text.
func (s *StringObj) RuneAt(i int) string {
	offset := s.offsetOf(i)
	if s.ascii {
		return s.data[offset : offset+1]
	}
	_, size := utf8.DecodeRuneInString(s.data[offset:])
	return s.data[offset : offset+size]
}

// Slice returns the characters from one position up to another. Both must
// be within the text or at its end, and in order.
func (s *StringObj) Slice(from, to int) string {
	start, end := s.offsetOf(from), s.offsetOf(to)
	return s.data[start:end]
}

// ArrayObj represents a heap-allocated array.
type ArrayObj struct {
	header   ObjHeader
	elements []Value
}

func (a *ArrayObj) Header() *ObjHeader {
	return &a.header
}

func (a *ArrayObj) String() string {
	return "[array]"
}

func (a *ArrayObj) Len() int {
	return len(a.elements)
}

func (a *ArrayObj) Get(i int) Value {
	if i < 0 || i >= len(a.elements) {
		return NewNull()
	}
	return a.elements[i]
}

func (a *ArrayObj) Set(i int, v Value) {
	if i >= 0 && i < cap(a.elements) {
		// Grow if needed
		for len(a.elements) <= i {
			a.elements = append(a.elements, NewNull())
		}
		a.elements[i] = v
	}
}

func (a *ArrayObj) Append(v Value) {
	a.elements = append(a.elements, v)
}

// Shape is what the objects with the same fields, added in the same
// order, have in common: which field is in which slot. An object holds its
// shape and the values; the names are kept once per shape.
//
// Shapes form a tree. Adding a field to an object moves it to the shape's
// child for that name, made the first time and found again afterwards, so
// that every value of a struct ends up with the very same shape. That is
// what lets an instruction remember where a field is (see fieldCache).
type Shape struct {
	keys  []string
	index map[string]int
	next  map[string]*Shape

	// What the objects of this shape are values of, which they have in
	// common as they have their fields: the name of the struct, or
	// "Enum.Variant" for a value of an enum, which is compared and printed
	// by what it holds. Empty for a plain object created from assembly.
	typeName string
	variant  bool

	// The shape of a value of that type without fields yet: where every
	// one of them starts, and where taking a field away starts again from
	root *Shape

	// On a root, the methods of the type found so far, for the chunk that
	// is asking (see chunkCache.method): the function of each method by
	// the number the chunk gave its name, plus one, and 0 for a method
	// not looked for yet.
	methods   []int32
	methodsOf *chunkCache

	// On a root, where each SWITCH that met a value of the type goes on
	// for it, by the number the chunk gave the SWITCH, plus one, and 0
	// for one that has not met the type yet.
	cases   []int32
	casesOf *chunkCache
}

// newRootShape makes the shape of the values of a type before they are
// given a field.
func newRootShape(typeName string, variant bool) *Shape {
	shape := &Shape{index: map[string]int{}, typeName: typeName, variant: variant}
	shape.root = shape
	return shape
}

// with returns the shape of an object of this shape given one more field.
func (s *Shape) with(key string) *Shape {
	if child, ok := s.next[key]; ok {
		return child
	}
	child := &Shape{
		keys:     append(s.keys[:len(s.keys):len(s.keys)], key),
		index:    make(map[string]int, len(s.keys)+1),
		typeName: s.typeName,
		variant:  s.variant,
		root:     s.root,
	}
	for name, slot := range s.index {
		child.index[name] = slot
	}
	child.index[key] = len(s.keys)
	if s.next == nil {
		s.next = map[string]*Shape{}
	}
	s.next[key] = child
	return child
}

// ObjectObj represents a heap-allocated object: a value of a struct or of
// an enum, or a plain object made from assembly. It is its shape and its
// values; everything the values of a type have in common is in the shape.
type ObjectObj struct {
	header ObjHeader
	shape  *Shape
	slots  []Value
}

// An object made with the number of its fields known comes with the room
// for them, in one piece: the object and its values are one allocation
// and lie together. One that outgrows its room moves its values elsewhere,
// as an array does.
type (
	objectOf1 struct {
		ObjectObj
		room [1]Value
	}
	objectOf2 struct {
		ObjectObj
		room [2]Value
	}
	objectOf3 struct {
		ObjectObj
		room [3]Value
	}
	objectOf4 struct {
		ObjectObj
		room [4]Value
	}
	objectOf6 struct {
		ObjectObj
		room [6]Value
	}
	objectOf8 struct {
		ObjectObj
		room [8]Value
	}
	objectOf12 struct {
		ObjectObj
		room [12]Value
	}
	objectOf16 struct {
		ObjectObj
		room [16]Value
	}
)

// newObject makes an object with room for at least capacity fields.
func newObject(shape *Shape, capacity int) *ObjectObj {
	var obj *ObjectObj
	switch {
	case capacity <= 0:
		obj = &ObjectObj{}
	case capacity == 1:
		whole := &objectOf1{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity == 2:
		whole := &objectOf2{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity == 3:
		whole := &objectOf3{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity == 4:
		whole := &objectOf4{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity <= 6:
		whole := &objectOf6{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity <= 8:
		whole := &objectOf8{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity <= 12:
		whole := &objectOf12{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	case capacity <= 16:
		whole := &objectOf16{}
		obj, whole.slots = &whole.ObjectObj, whole.room[:0]
	default:
		obj = &ObjectObj{slots: make([]Value, 0, capacity)}
	}
	obj.header.Type = ObjTypeObject
	obj.shape = shape
	return obj
}

// roomFor is the room newObject gives for a number of fields.
func roomFor(capacity int) int {
	switch {
	case capacity <= 0:
		return 0
	case capacity <= 4:
		return capacity
	case capacity <= 6:
		return 6
	case capacity <= 8:
		return 8
	case capacity <= 12:
		return 12
	case capacity <= 16:
		return 16
	}
	return capacity
}

// Third operand of NEWOBJ: what the object is, in its two lowest bits.
const (
	ObjectIsNamed   = 1 // A value of a struct; B is the constant of its name
	ObjectIsVariant = 2 // A value of an enum; B is the constant of "Enum.Variant"

	// The rest of the operand is for the compiler, which knows more about
	// the object than its name:
	//
	// ObjectIsConstant marks a value that is never given a field, of a
	// variant that holds nothing: every such value is the same, so one is
	// made and handed out each time.
	ObjectIsConstant = 4

	// From ObjectFieldsShift up, the number of fields the object is about
	// to be given (0 if not known), so that it is made with room for them.
	ObjectFieldsShift = 3
	ObjectKindMask    = 3
	MaxObjectFields   = MaxOperand >> ObjectFieldsShift
)

// TypeName returns the name of the struct the object is a value of.
func (o *ObjectObj) TypeName() string {
	return o.shape.typeName
}

func (o *ObjectObj) Header() *ObjHeader {
	return &o.header
}

func (o *ObjectObj) String() string {
	return "[object]"
}

func (o *ObjectObj) Get(key string) (Value, bool) {
	if slot, ok := o.shape.index[key]; ok {
		return o.slots[slot], true
	}
	return NewNull(), false
}

// Set gives a field a value, adding the field if the object has none of
// that name. It does not account for the memory a new field takes: inside
// the VM, use setField.
func (o *ObjectObj) Set(key string, v Value) {
	if slot, ok := o.shape.index[key]; ok {
		o.slots[slot] = v
		return
	}
	o.shape = o.shape.with(key)
	o.slots = append(o.slots, v)
}

// Delete takes a field away, and reports whether there was one. The fields
// left keep their order.
func (o *ObjectObj) Delete(key string) bool {
	at, ok := o.shape.index[key]
	if !ok {
		return false
	}
	shape := o.shape.root
	for i, name := range o.shape.keys {
		if i != at {
			shape = shape.with(name)
		}
	}
	o.slots = append(o.slots[:at], o.slots[at+1:]...)
	o.shape = shape
	return true
}

// Len is the number of fields.
func (o *ObjectObj) Len() int {
	return len(o.slots)
}

// Keys lists the names of the fields, in the order they were added.
func (o *ObjectObj) Keys() []string {
	return append([]string(nil), o.shape.keys...)
}

// Fields returns a copy of all fields (for debugging/inspection).
func (o *ObjectObj) Fields() map[string]Value {
	fieldsCopy := make(map[string]Value, len(o.slots))
	for i, key := range o.shape.keys {
		fieldsCopy[key] = o.slots[i]
	}
	return fieldsCopy
}

// FunctionObj represents a heap-allocated first-class function.
// Supports closures with upvalues for variable capture.
type FunctionObj struct {
	header   ObjHeader
	chunk    *Chunk             // Owning chunk containing prototypes and code
	protoIdx int                // Index into chunk.Prototypes
	proto    *FunctionPrototype // The prototype itself
	upvalues []*UpvalueObj      // Captured variables (can be empty for non-closures)
}

func (f *FunctionObj) Header() *ObjHeader {
	return &f.header
}

func (f *FunctionObj) String() string {
	return "[function]"
}

// Proto returns the function prototype metadata.
func (f *FunctionObj) Proto() *FunctionPrototype {
	return f.proto
}

// Upvalues returns the captured upvalues for this function.
func (f *FunctionObj) Upvalues() []*UpvalueObj {
	return f.upvalues
}

// SetUpvalue sets an upvalue at the given index.
func (f *FunctionObj) SetUpvalue(idx int, upval *UpvalueObj) {
	if idx >= 0 && idx < len(f.upvalues) {
		f.upvalues[idx] = upval
	}
}

// ExternFunctionObj represents a heap-allocated external (host) function.
// External functions are implemented in Go and called from bytecode.
type ExternFunctionObj struct {
	header   ObjHeader
	name     string     // Function name
	function ExternFunc // Go function pointer
	argCount int        // Expected argument count

	// Type of the function as a cast descriptor; empty if not declared
	signature string
}

func (ef *ExternFunctionObj) Header() *ObjHeader {
	return &ef.header
}

func (ef *ExternFunctionObj) String() string {
	return "[extern:" + ef.name + "]"
}

func (ef *ExternFunctionObj) Name() string {
	return ef.name
}

func (ef *ExternFunctionObj) Function() ExternFunc {
	return ef.function
}

func (ef *ExternFunctionObj) ArgCount() int {
	return ef.argCount
}

// UpvalueObj represents a captured variable in a closure.
// Upvalues can be in two states:
// 1. Open: the variable is still a register of a call in progress
// 2. Closed: the call has returned, and the upvalue holds the value itself
//
// An open upvalue names its register by the stack it is on and its place
// there, not by its address: a stack that grows moves.
type UpvalueObj struct {
	header ObjHeader
	stack  *regStack // The stack the variable is on (nil when closed)
	slot   int       // Its place on that stack
	closed Value     // Closed-over value (used when stack == nil)
}

func (u *UpvalueObj) Header() *ObjHeader {
	return &u.header
}

func (u *UpvalueObj) String() string {
	return "[upvalue]"
}

// IsOpen returns true if the upvalue is still pointing to a stack location.
func (u *UpvalueObj) IsOpen() bool {
	return u.stack != nil
}

// Get returns the current value of the upvalue.
func (u *UpvalueObj) Get() Value {
	if u.stack != nil {
		return u.stack.vals[u.slot]
	}
	return u.closed
}

// Set updates the value of the upvalue.
func (u *UpvalueObj) Set(v Value) {
	if u.stack != nil {
		u.stack.vals[u.slot] = v
	} else {
		u.closed = v
	}
}

// Close moves the upvalue from stack to heap.
// This is called when the stack frame containing the variable is about to be destroyed.
func (u *UpvalueObj) Close() {
	if u.stack != nil {
		u.closed = u.stack.vals[u.slot]
		u.stack = nil
	}
}

// IndexObj says where each key of a dictionary is among its keys: what
// makes finding one take the same time however many there are. A
// dictionary of a few keys has none, and looks through them.
type IndexObj struct {
	header ObjHeader
	at     map[string]int32

	// What the index is counted for against the limit: it grows with
	// every key, and what was counted is what is taken off when it goes
	counted uint64
}

func (i *IndexObj) Header() *ObjHeader {
	return &i.header
}

func (i *IndexObj) String() string {
	return "[index]"
}
