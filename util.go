package main

func blockKey(block string, valid bool) string {
	if !valid || block == "" {
		return "nil"
	}
	return block
}

func formatBlockForLog(block string, valid bool) string {
	if !valid || block == "" {
		return "nil"
	}
	return block
}
