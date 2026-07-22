package compose

type StreamEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Done    bool   `json:"done"`
}

type Streamer struct{}

func NewStreamer() *Streamer { return &Streamer{} }

func (s *Streamer) Stream(action, context, instruction, language, tone string, ch chan<- StreamEvent) {
	defer close(ch)
	ch <- StreamEvent{Type: "meta", Content: "starting"}
	ch <- StreamEvent{Type: "chunk", Content: ""}
	ch <- StreamEvent{Type: "done", Content: "", Done: true}
}
