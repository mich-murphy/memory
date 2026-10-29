package adapters

// ClassifyLogical classifies an already admitted declared coordinate without
// consulting whether its logical root still exists. Pi uses the same structural
// interpretation for ordinary and declared coordinates.
func ClassifyLogical(a Adapter, path, root string) (SessionFile, bool) {
	return a.Classify(path, root)
}
