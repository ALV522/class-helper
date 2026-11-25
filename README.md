# Class Helper · 课堂助手（Windows）

Windows 原生桌面程序，不会打开浏览器，也不需要运行 Web 服务器。主要功能是边录音、边转写、边写笔记，并在课堂笔记中粘贴或插入 PPT 照片。

> **开发状态：v0.1.0 Preview**。已完成 Linux→Windows amd64 交叉编译和 Go 单元测试；还需要在真实 Windows 电脑上验证麦克风采集、RichEdit 图片嵌入和 90 分钟持续录制。**不要在没有备用录音设备时将其作为唯一课堂记录工具。**

## 下载

- 便捷版：[最新测试版 EXE（Release）](https://github.com/ALV522/class-helper/releases/tag/nightly)；如果没有发布成功，可进入 [Actions](https://github.com/ALV522/class-helper/actions) → `Build Windows EXE` → 最新运行 → Artifacts → `ClassHelper-Windows`。
- 不需要安装 Python、Go、Node.js 或浏览器。
- Windows 10/11 x64，使用 Windows 自带的 RichEdit 和 WinMM。
- Windows SmartScreen 可能因为程序尚未签名而提示未知发布者。请先确认文件来源；该项目不会要求关闭安全软件。

## 课堂使用

1. 双击 `ClassHelper.exe`，输入课程名（如“组织管理研究方法”）。
2. 可选：在右上角填写 DeepSeek API Key。**不会写入本地配置或导出内容，也不会上传到 GitHub**。也可通过当前用户的环境变量 `DEEPSEEK_API_KEY` 提供。
3. 点「开始录音」并同意 Windows 麦克风权限。左侧是可编辑的课堂富文本笔记，右侧是接连出现的转写段落。
4. 记笔记时，可以用 `Ctrl+V` 粘贴图片或按「插入 PPT 图片」选择电脑上的 PNG/JPEG；插入目标是左侧当前光标所在处。照片会保存在笔记的 RTF 文档中。
5. 右侧转写可以随时手动修改。新的 AI 识别结果只**追加**到末尾，不主动改写旧内容。
6. 点「停止录音」生成完整 `audio.wav`；想要汇总文字时点「AI 总结」。
7. 点「导出 ZIP」得到 WAV 录音、RTF 图文笔记、TXT 转写、Markdown 总结、课堂元数据及 ASR 分段备份。

## 文件存放位置

默认目录为 `%USERPROFILE%\Documents\ClassHelper`：

```text
ClassHelper/
├── sessions/
│   └── 20261125_193000/
│       ├── audio.wav           # 完整录音，每约 5 秒修订头部并同步落盘
│       ├── notes.rtf           # 可编辑图文笔记（包含嵌入图片）
│       ├── transcript.txt      # 可人工修改的实时转写
│       ├── summary.md          # AI 总结
│       ├── session.json        # 课程名、时间、录音时长
│       └── chunks/
│           ├── 00000.wav       # 12 秒左右音频，网络故障时可补转写
│           ├── 00000.wav.start # 片段在音频中的起始秒数
│           └── 00000.wav.done  # 成功转写的确认标记
└── exports/
    └── 20261125_194500_class.zip
```

关闭时保存，编辑期间约每 5 秒自动保存。断网不影响本地录音和记笔记；网络恢复后点击「补转写」。转写请求会把片段音频发送至 DeepSeek 服务端（请遵守课堂录音要求及有关隐私规则）。

## DeepSeek 接口说明

- 语音识别：`POST https://api.deepseek.com/v1/tools/asr`，发送 16 kHz、16-bit PCM、mono WAV，约每 12 秒提交一次。该服务**不支持原生流式返回**，所以字幕是近实时分段出现，不是逐字即时显示。
- 文字总结：`POST https://api.deepseek.com/chat/completions`，模型 `deepseek-flash`。长课堂转写会被分段总结再合并。
- 2026-11-25 官方说明：[Speech-to-Text](https://api-docs.deepseek.com/guides/asr/) / [Chat API](https://api-docs.deepseek.com/guides/build_a_chatbot/)。
- 没有 Key 时，录音、编辑笔记、插入图片、保存和导出仍然可用，但不会产生 AI 转写或总结。

## 已知限制

- **单次录音只能使用系统默认麦克风**；系统声音捕捉、多个麦克风、扬声器回声消除尚未支持。部分设备可能不支持 16kHz 的 WinMM 麦克风格式。
- RTF 可以保存和重新打开图文笔记，但还没有 DOCX、PDF 或 Obsidian 级别 Markdown 图片导出；需要时可用 Word/写字板打开 RTF 另存为其他格式。
- 录音与照片目前按课堂文件归档，没有照片→秒级录音联动和图片 OCR。ASR 时间戳以音频片段开始时间为准，不是逐词时间戳。
- 图片粘贴依赖 Windows RichEdit 的剪贴板支持，在部分剪贴板格式下可能无法正常插入。可以使用「插入 PPT 图片」将 JPG/PNG 转成兼容的 BMP/DIB 粘贴。
- AI 总结仅参考转写文字，**不会自动读取笔记中的图片内容**。
- 目前仅 Windows amd64；尚无签名安装程序、自动更新、云同步及多设备联动。

## 源码构建

Go 1.22+：

```powershell
go test ./...
go build -trimpath -ldflags="-H=windowsgui -s -w" -o ClassHelper.exe .
```

不依赖外部 Go 模块。GitHub Actions 会在 `windows-latest` 上运行测试，构建 `ClassHelper.exe`，保存 Artifact 并尝试更新 `nightly` 预览 Release。

## 隐私与许可

课程录音、转写和笔记均存储在本地。只有在配置 DeepSeek Key 且调用 AI 功能时，录音片段或转写文本才会发送至该 API。代码 MIT License；用户对自己记录的音频和图片享有相应权利，同时应遵守教师、学校的录音与传播规定。
