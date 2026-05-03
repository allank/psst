package embedder

import (
	"fmt"
	"math"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/allank/psst/internal/tokenizer"
)

var ortInitOnce sync.Once
var ortInitErr error

// Embedder is the interface for embedding text to a float32 vector.
type Embedder interface {
	Embed(text string) ([]float32, error)
	Close() error
}

// ONNXEmbedder uses ONNX Runtime with all-MiniLM-L6-v2.
type ONNXEmbedder struct {
	session *ort.DynamicAdvancedSession
	tok     *tokenizer.Tokenizer
}

func NewONNX(modelPath, tokenizerPath string) (*ONNXEmbedder, error) {
	ortInitOnce.Do(func() {
		ort.SetSharedLibraryPath(onnxLibPath())
		ortInitErr = ort.InitializeEnvironment()
	})
	if ortInitErr != nil {
		return nil, fmt.Errorf("onnxruntime init: %w", ortInitErr)
	}
	tok, err := tokenizer.LoadFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}
	inputNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	outputNames := []string{"last_hidden_state"}
	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("onnx session: %w", err)
	}
	return &ONNXEmbedder{session: session, tok: tok}, nil
}

func (e *ONNXEmbedder) Embed(text string) ([]float32, error) {
	enc := e.tok.Encode(text, 512)
	seqLen := int64(len(enc.InputIDs))

	inputIDs, err := ort.NewTensor(ort.NewShape(1, seqLen), enc.InputIDs)
	if err != nil {
		return nil, err
	}
	defer inputIDs.Destroy()

	attnMask, err := ort.NewTensor(ort.NewShape(1, seqLen), enc.AttentionMask)
	if err != nil {
		return nil, err
	}
	defer attnMask.Destroy()

	tokenTypes, err := ort.NewTensor(ort.NewShape(1, seqLen), enc.TokenTypeIDs)
	if err != nil {
		return nil, err
	}
	defer tokenTypes.Destroy()

	outputData := make([]float32, seqLen*384)
	output, err := ort.NewTensor(ort.NewShape(1, seqLen, 384), outputData)
	if err != nil {
		return nil, err
	}
	defer output.Destroy()

	if err := e.session.Run([]ort.Value{inputIDs, attnMask, tokenTypes}, []ort.Value{output}); err != nil {
		return nil, fmt.Errorf("onnx run: %w", err)
	}

	hidden := output.GetData()
	return meanPoolAndNormalize(hidden, enc.AttentionMask, int(seqLen)), nil
}

func (e *ONNXEmbedder) Close() error {
	return e.session.Destroy()
}

// meanPoolAndNormalize computes masked mean of token embeddings and L2-normalizes.
func meanPoolAndNormalize(hidden []float32, mask []int64, seqLen int) []float32 {
	const dims = 384
	pooled := make([]float32, dims)
	var count float32
	for i := 0; i < seqLen; i++ {
		if mask[i] == 0 {
			continue
		}
		count++
		for d := 0; d < dims; d++ {
			pooled[d] += hidden[i*dims+d]
		}
	}
	if count > 0 {
		for d := range pooled {
			pooled[d] /= count
		}
	}
	var norm float64
	for _, v := range pooled {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for d := range pooled {
			pooled[d] = float32(float64(pooled[d]) / norm)
		}
	}
	return pooled
}

// EmbeddedModel returns the model bytes embedded at build time (empty in dev builds).
func EmbeddedModel() []byte { return embeddedModel }

// EmbeddedTokenizer returns the tokenizer bytes embedded at build time (empty in dev builds).
func EmbeddedTokenizer() []byte { return embeddedTokenizer }

// NewFromBytes creates an embedder from in-memory model and tokenizer bytes
// by writing them to temp files (onnxruntime_go requires file paths).
func NewFromBytes(modelData, tokenizerData []byte) (*ONNXEmbedder, error) {
	mf, err := os.CreateTemp("", "psst-model-*.onnx")
	if err != nil {
		return nil, err
	}
	defer os.Remove(mf.Name())
	if _, err := mf.Write(modelData); err != nil {
		mf.Close()
		return nil, err
	}
	mf.Close()

	tf, err := os.CreateTemp("", "psst-tokenizer-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tf.Name())
	if _, err := tf.Write(tokenizerData); err != nil {
		tf.Close()
		return nil, err
	}
	tf.Close()

	return NewONNX(mf.Name(), tf.Name())
}
