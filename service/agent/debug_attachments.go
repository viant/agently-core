package agent

import (
	"log"
	"os"

	"github.com/viant/agently-core/internal/logx"
)

func debugAttachmentf(format string, args ...interface{}) {
	if !logx.Enabled() {
		return
	}
	log.New(os.Stdout, "", log.LstdFlags).Printf("[attachments] "+format, args...)
}
