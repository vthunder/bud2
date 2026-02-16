package filter

import (
	"regexp"
	"strings"
)

// DialogueAct represents the pragmatic function of an utterance
type DialogueAct string

const (
	ActBackchannel DialogueAct = "backchannel" // acknowledgment signals
	ActQuestion    DialogueAct = "question"    // interrogative
	ActStatement   DialogueAct = "statement"   // declarative
	ActCommand     DialogueAct = "command"     // imperative/request
	ActGreeting    DialogueAct = "greeting"    // social opening/closing
	ActUnknown     DialogueAct = ""            // not classified
)

// backchannelPatterns are common acknowledgment signals
var backchannelPatterns = []string{
	`^(yes|yeah|yep|yup|ya|yea)\.?$`,
	`^(no|nope|nah)\.?$`,
	`^(ok|okay|k|kk)\.?$`,
	`^(sure|right|correct|exactly)\.?$`,
	`^(got it|gotcha|understood|copy)\.?$`,
	`^(thanks|thank you|thx|ty)\.?!?$`,
	`^(cool|nice|great|awesome|perfect)\.?!?$`,
	`^(uh[ -]?huh|mhm|mm|hmm)\.?$`,
	`^(sounds good|looks good|lgtm)\.?$`,
	`^(i see|ah|oh)\.?$`,
	`^(👍|✓|✔|✅|🙌|👌|💯)$`,
	`^!?$`, // empty or just punctuation
}

// greetingPatterns are social opening/closing signals
var greetingPatterns = []string{
	`^(hi|hey|hello|yo|sup|heya)\.?!?$`,
	`^good (morning|afternoon|evening|night)\.?!?$`,
	`^(gm|gn)\.?!?$`,
	`^(bye|goodbye|later|cya|see ya|ttyl)\.?!?$`,
	`^(👋|🌅|🌙|✌️)$`,
}

// questionPatterns indicate interrogative utterances
var questionPatterns = []string{
	`\?$`,                              // ends with question mark
	`^(what|where|when|who|why|how)\b`, // starts with wh-word
	`^(can|could|would|should|will|do|does|did|is|are|have|has)\s+(you|i|we|it|they)\b`,
}

// commandPatterns indicate imperative utterances
var commandPatterns = []string{
	`^(please|pls)\b`,
	`^(can you|could you|would you)\b`,
	`^(run|do|make|create|add|remove|delete|show|list|get|set|update|fix|check)\b`,
}

var (
	compiledBackchannel []*regexp.Regexp
	compiledGreeting    []*regexp.Regexp
	compiledQuestion    []*regexp.Regexp
	compiledCommand     []*regexp.Regexp
)

func init() {
	// Compile patterns once at startup
	compiledBackchannel = compilePatterns(backchannelPatterns)
	compiledGreeting = compilePatterns(greetingPatterns)
	compiledQuestion = compilePatterns(questionPatterns)
	compiledCommand = compilePatterns(commandPatterns)
}

func compilePatterns(patterns []string) []*regexp.Regexp {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err == nil {
			result = append(result, re)
		}
	}
	return result
}

// ClassifyDialogueAct determines the pragmatic function of an utterance
func ClassifyDialogueAct(content string) DialogueAct {
	content = strings.TrimSpace(content)
	if content == "" {
		return ActBackchannel // Empty is a backchannel
	}

	// Check for backchannels first (most specific)
	if matchesAny(content, compiledBackchannel) {
		return ActBackchannel
	}

	// Check for greetings
	if matchesAny(content, compiledGreeting) {
		return ActGreeting
	}

	// Check for questions
	if matchesAny(content, compiledQuestion) {
		return ActQuestion
	}

	// Check for commands
	if matchesAny(content, compiledCommand) {
		return ActCommand
	}

	// Default to statement
	return ActStatement
}

func matchesAny(content string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(content) {
			return true
		}
	}
	return false
}

// IsBackchannel returns true if the content is a simple acknowledgment
func IsBackchannel(content string) bool {
	return ClassifyDialogueAct(content) == ActBackchannel
}

// IsLowInfo returns true if the content is low-information (backchannel or greeting)
func IsLowInfo(content string) bool {
	act := ClassifyDialogueAct(content)
	return act == ActBackchannel || act == ActGreeting
}

// ShouldAttachToPrevious returns true if this message should be attached
// to the previous turn rather than standing alone
func ShouldAttachToPrevious(content string) bool {
	act := ClassifyDialogueAct(content)

	// Backchannels should attach to what they're responding to
	if act == ActBackchannel {
		return true
	}

	// Very short non-question messages should attach
	if act != ActQuestion && len(strings.Fields(content)) <= 3 {
		return true
	}

	return false
}
