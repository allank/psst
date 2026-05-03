//go:build linux

package embedder

func onnxLibPath() string {
	if p := onnxLibEnv(); p != "" {
		return p
	}
	return "/usr/local/lib/libonnxruntime.so"
}
