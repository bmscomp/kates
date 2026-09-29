package output

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
