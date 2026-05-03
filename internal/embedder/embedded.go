//go:build embedmodel

package embedder

import _ "embed"

//go:embed model/model.onnx
var embeddedModel []byte

//go:embed model/tokenizer.json
var embeddedTokenizer []byte
