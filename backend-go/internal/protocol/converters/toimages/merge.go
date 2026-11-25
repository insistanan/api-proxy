package toimages

// 终态只覆盖实际提供的字段；图片仅在早期事件出现时仍保留其内容。
func MergeFinalItem(previous, final ImageItem) ImageItem {
	if final.ResultJSON == "" {
		final.ResultJSON = previous.ResultJSON
	}
	if final.RevisedPrompt == "" {
		final.RevisedPrompt = previous.RevisedPrompt
	}
	if final.OutputFormat == "" {
		final.OutputFormat = previous.OutputFormat
	}
	if final.Background == "" {
		final.Background = previous.Background
	}
	if final.Size == "" {
		final.Size = previous.Size
	}
	if final.Quality == "" {
		final.Quality = previous.Quality
	}
	if len(final.Usage) == 0 {
		final.Usage = previous.Usage
	}
	return final
}
