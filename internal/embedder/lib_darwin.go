//go:build darwin

package embedder

func onnxLibPath() string {
	if p := onnxLibEnv(); p != "" {
		return p
	}
	return "/usr/local/lib/libonnxruntime.dylib"
}
