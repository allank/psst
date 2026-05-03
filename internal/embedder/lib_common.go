package embedder

import "os"

func onnxLibEnv() string {
	return os.Getenv("PSST_ONNX_LIB")
}
