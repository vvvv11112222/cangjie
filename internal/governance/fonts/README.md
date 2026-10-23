# PDF 中文字体

报告使用内嵌的 Noto Sans SC，独立于服务器和读者电脑安装的字体。PDF 中只嵌入实际使用字形及 Unicode 映射，支持复制、检索和打印。

- 上游：[Google Fonts Noto Sans SC](https://github.com/google/fonts/tree/main/ofl/notosanssc)
- 许可：同目录 `OFL.txt`（SIL Open Font License 1.1）。
- `NotoSansSC-Regular.ttf` 由上游 `NotoSansSC[wght].ttf` 通过 fontTools `instantiateVariableFont(font, {'wght': 400})` 生成静态常规字重，未缩减中文字符集。
- 构建时通过 Go embed 打包；运行、导出、Docker 部署时不下载字体，也不依赖本机 Windows 字体目录。

静态字体 SHA-256：`f2f579bcbc087ebd973d40318ff8eeb4a6b10ab2c22de6ef21b42e99c7bb2e04`。
