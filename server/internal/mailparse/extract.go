package mailparse

import (
	"bytes"
	"fmt"

	"github.com/jhillyerd/enmime/v2"
)

// ExtractPart returns the decoded content of the MIME part with the given PartID
// (as recorded at ingest). Attachments are served this way instead of being
// stored twice.
func ExtractPart(raw []byte, partID string) ([]byte, error) {
	parser := enmime.NewParser(enmime.SkipMalformedParts(true))
	root, err := parser.ReadParts(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}
	if p := root.DepthMatchFirst(func(p *enmime.Part) bool { return p.PartID == partID }); p != nil {
		return p.Content, nil
	}
	return nil, fmt.Errorf("part %q not found", partID)
}
