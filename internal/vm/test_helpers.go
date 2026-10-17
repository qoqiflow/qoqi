package vm

// Helper function to add a main prototype to a chunk
// This wraps existing test chunks with the new FunctionPrototype requirement
func addMainProto(chunk *Chunk, registerCount int, argCount int) {
	codeSize := len(chunk.Code)
	mainProto := FunctionPrototype{
		Name:          "main",
		CodeStart:     0,
		CodeSize:      codeSize,
		RegisterCount: registerCount,
		ArgCount:      argCount,
	}
	chunk.Prototypes = append(chunk.Prototypes, mainProto)
}
