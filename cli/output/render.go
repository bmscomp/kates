package output

import "io"

// The -o json contract: while JSON mode is on, stdout carries the JSON result
// and nothing else. A script reads stdout with jq; a warning or a hint that
// lands there breaks the parse, and a banner before the document did, in
// kates doctor. So Success, Warn and Hint go to stderr in JSON mode, where
// they are still seen, and Banner prints nothing: it has no place in a JSON
// stream and nothing to say on stderr. Error always went to stderr.
//
// The root command sets the mode once, after it has resolved -o, KATES_OUTPUT
// and the context's default output. A command never sets it; a test may.

var jsonMode bool

// JSONMode turns the -o json contract on or off.
func JSONMode(on bool) {
	jsonMode = on
}

// IsJSON reports whether the -o json contract is on.
func IsJSON() bool {
	return jsonMode
}

// noteWriter is where Success, Warn and Hint write: stdout, or stderr while
// JSON mode keeps stdout for the result.
func noteWriter() io.Writer {
	if jsonMode {
		return Err
	}
	return Out
}

// Render handles conditional output formatting based on the jsonEnabled flag.
// If jsonEnabled is true, it renders the data parameter as JSON to stdout.
// Otherwise, it calls the renderTable function to display the tabular output.
func Render(jsonEnabled bool, data interface{}, renderTable func()) {
	if jsonEnabled {
		JSON(data)
		return
	}
	renderTable()
}
