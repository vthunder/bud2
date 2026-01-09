package executive

import (
	"math/rand"
)

// windowWords is a list of friendly, memorable words for tmux window names.
// These are chosen to be short, distinct, and easy to read in tmux.
var windowWords = []string{
	// Animals
	"badger", "falcon", "gopher", "otter", "raven",
	"tiger", "wolf", "fox", "hawk", "bear",
	"owl", "lynx", "pike", "seal", "crane",
	"finch", "heron", "swift", "wren", "robin",

	// Food
	"cheese", "lemon", "mango", "olive", "peach",
	"apple", "cherry", "grape", "melon", "plum",
	"basil", "sage", "thyme", "mint", "ginger",
	"cocoa", "honey", "maple", "toast", "bread",

	// Tools/Objects
	"hammer", "anvil", "chisel", "forge", "lathe",
	"bolt", "gear", "lever", "prism", "lens",
	"compass", "beacon", "anchor", "helm", "mast",
	"quill", "scroll", "tome", "rune", "sigil",

	// Nature
	"nebula", "comet", "nova", "quasar", "pulsar",
	"brook", "creek", "delta", "fjord", "grove",
	"ridge", "summit", "valley", "canyon", "mesa",
	"frost", "storm", "breeze", "ember", "spark",

	// Tech-ish
	"kernel", "socket", "buffer", "cache", "queue",
	"token", "cipher", "codec", "pixel", "voxel",
	"vector", "matrix", "tensor", "scalar", "graph",
	"node", "edge", "vertex", "mesh", "grid",

	// Colors/Materials
	"cobalt", "copper", "bronze", "silver", "jade",
	"amber", "coral", "ivory", "onyx", "opal",
	"ruby", "topaz", "pearl", "quartz", "flint",
	"slate", "marble", "granite", "basite", "shale",
}

// randomWord returns a random word from the word list
func randomWord() string {
	return windowWords[rand.Intn(len(windowWords))]
}
