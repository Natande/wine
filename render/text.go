package render

import (
	"log"
	"net/http"

	ghttp "github.com/justintan/gox/http"
	gio "github.com/justintan/gox/io"
)

// Text render text into writer
func Text(writer http.ResponseWriter, text string) {
	writer.Header()["Content-Type"] = []string{ghttp.MIMETEXT + "; charset=utf-8"}
	data := []byte(text)
	err := gio.Write(writer, data)
	if err != nil {
		log.Println("[WINE] Render error:", err)
	}
}
