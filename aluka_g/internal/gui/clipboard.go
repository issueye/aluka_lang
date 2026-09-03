package gui

// ClipboardReadText 读取系统剪贴板中的纯文本内容。
func ClipboardReadText() (string, error) {
	return clipboardReadText()
}

// ClipboardWriteText 将指定纯文本写入系统剪贴板。
func ClipboardWriteText(text string) error {
	return clipboardWriteText(text)
}
