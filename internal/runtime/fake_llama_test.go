package runtime

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// fakeLlama serves the llama-server API on the --port given in args, with the --api-key given. Vectors come from
// a hash of the model file name and the input, so two model files give different vectors (one vector space each).
// An image whose data is not base64 gets HTTP 500, like the real server for a bad image.
func fakeLlama(args []string) {
	var port, key, model string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--port":
			port = args[i+1]
		case "--api-key":
			key = args[i+1]
		case "-m":
			model = args[i+1]
		case "--mmproj":
			model += "|" + args[i+1]
		}
	}
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, `{"error":{"message":"Invalid API Key","code":401}}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var in struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		// One token per rune. A rune of more than one byte becomes a byte-array piece, as byte fallback does.
		var toks []map[string]any
		for i, rn := range in.Content {
			piece := string(rn)
			var p any = piece
			if utf8.RuneLen(rn) > 1 && rn > 0x7f {
				bs := []int{}
				for _, b := range []byte(piece) {
					bs = append(bs, int(b))
				}
				p = bs
			}
			toks = append(toks, map[string]any{"id": i, "piece": p})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": toks})
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var in struct {
			Input []json.RawMessage `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var data []map[string]any
		for i, raw := range in.Input {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				var item struct {
					Content []struct {
						ImageURL struct {
							URL string `json:"url"`
						} `json:"image_url"`
					} `json:"content"`
				}
				if json.Unmarshal(raw, &item) != nil || len(item.Content) == 0 {
					http.Error(w, `{"error":{"message":"bad input","code":400}}`, http.StatusBadRequest)
					return
				}
				url := item.Content[0].ImageURL.URL
				_, b64, _ := strings.Cut(url, ";base64,")
				raw, err := base64.StdEncoding.DecodeString(b64)
				if marker, ok := strings.CutPrefix(string(raw), "PARTIAL:"); ok && err == nil {
					// Send HTTP 200 and the start of the JSON body, create marker, and never finish the reply.
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, `{"data":[{"index":0,"embedding":[`)
					w.(http.Flusher).Flush()
					_ = os.WriteFile(marker, nil, 0o644)
					time.Sleep(time.Hour)
					return
				}
				if err != nil || strings.HasPrefix(string(raw), "BAD") {
					http.Error(w, `{"error":{"message":"Failed to load image or audio file","type":"server_error","code":500}}`, http.StatusInternalServerError)
					return
				}
				text = "image:" + b64
			}
			data = append(data, map[string]any{"index": i, "embedding": fakeVector(model, text)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "usage": map[string]any{"prompt_tokens": len(in.Input)}})
	})
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		panic(err)
	}
}

// fakeVector returns a unit vector of 768 values derived from model and text.
func fakeVector(model, text string) []float32 {
	v := make([]float32, embeddingDims)
	var norm float64
	for i := range v {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", model, text, i)))
		x := float64(binary.LittleEndian.Uint32(h[:4]))/math.MaxUint32*2 - 1
		v[i] = float32(x)
		norm += x * x
	}
	s := float32(1 / math.Sqrt(norm))
	for i := range v {
		v[i] *= s
	}
	return v
}
