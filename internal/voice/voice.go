// Package voice implements dictation: microphone capture streams to xAI
// streaming STT and transcripts land in the prompt box.
//
// The pipeline matches grok-build's xai-grok-voice crate: Ctrl+Space /
// F8 toggles capture (hold-to-talk when the terminal reports key
// releases), interim text paints as a highlighted overlay, and finals
// append to the textarea. Nothing is auto-sent.
package voice
