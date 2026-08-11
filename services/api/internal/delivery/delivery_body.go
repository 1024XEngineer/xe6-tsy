package delivery

import (
	"fmt"
	"strings"
)

const anonymousSpeakerLabel = "Anonymous speaker"

func formatDeliveryTurns(turns []FinalTurnSnapshot) string {
	var builder strings.Builder
	for index, turn := range turns {
		if index > 0 {
			builder.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&builder, "Turn: %s\n", turn.TurnID)
		fmt.Fprintf(&builder, "Speaker: %s\n", anonymousSpeakerLabel)
		if turn.SourceText != "" {
			fmt.Fprintf(&builder, "Source: %s\n", turn.SourceText)
		}
		if turn.TranslatedText != "" {
			fmt.Fprintf(&builder, "Translation: %s\n", turn.TranslatedText)
		}
	}
	return builder.String()
}
