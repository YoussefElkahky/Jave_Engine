<div align="center">

# 🧠 Jave Engine

**The core engine behind Jave — a local AI assistant built for private, intelligent interaction.**

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Status](https://img.shields.io/badge/status-in%20development-orange)
![Privacy](https://img.shields.io/badge/privacy-100%25%20local-success)
![Languages](https://img.shields.io/badge/languages-English%20%7C%20Arabic-blue)

</div>

---

## 📖 About

**Jave Engine** is the modular foundation of **Jave**, a personal AI assistant that runs entirely on your own machine. Written in Go, it brings together local AI models, Arabic language processing, speech recognition and synthesis, browser integration, and extensible system automation — without sending your data to the cloud.

The goal is simple: an assistant that is **private**, **fast**, **bilingual**, and **hackable**.

## ✨ Features

- 🔒 **Local-first AI** — runs on local models, so your conversations and data stay on your device.
- 🌍 **Arabic language processing** — built to understand and respond in Arabic alongside English.
- 🎙️ **Speech recognition** — talk to Jave using your voice.
- 🔊 **Speech synthesis** — Jave talks back using [Piper](https://github.com/rhasspy/piper) text-to-speech.
- 🌐 **Browser integration** — a companion browser extension lets Jave interact with the web.
- ⚙️ **Extensible system automation** — add new commands and capabilities without touching the core.
- 🧩 **Modular design** — each capability is a separate component you can swap, extend, or disable.

## 🗂️ Project Structure

```
Jave_Engine/
├── jave/              # Core engine source code
├── jave-extension/    # Browser extension for web integration
├── piper_install/     # Piper TTS installation files
├── go.mod             # Go module definition
└── test.go            # Test entry point
```

## 📋 Requirements

- **Go** 1.26 or newer
- A local AI runtime/model (e.g. [Ollama](https://ollama.com))
- **Piper** for speech synthesis (see `piper_install/`)
- A speech recognition backend (e.g. [whisper.cpp](https://github.com/ggerganov/whisper.cpp))
- A Chromium-based browser (for the extension)
- Linux recommended (developed and tested on Linux)

> ⚠️ Adjust this list to match the exact dependencies your build uses.

## 🚀 Getting Started

### 1. Clone the repository

```bash
git clone https://github.com/YoussefElkahky/Jave_Engine.git
cd Jave_Engine
```

### 2. Install Piper (text-to-speech)

Follow the instructions in the `piper_install/` directory to set up Piper and download a voice model.

### 3. Set up your local model

```bash
# Example using Ollama
ollama pull <your-model>
```

### 4. Build and run

```bash
go build -o jave ./jave
./jave
```

Or run directly:

```bash
go run ./jave
```

### 5. Load the browser extension

1. Open your browser's extensions page (`chrome://extensions`).
2. Enable **Developer mode**.
3. Click **Load unpacked** and select the `jave-extension/` folder.

## 🏗️ Architecture

```
          ┌──────────────────────────────┐
 Voice ──▶│  Speech Recognition          │
          └──────────────┬───────────────┘
                         ▼
          ┌──────────────────────────────┐
 Text  ──▶│  Jave Core Engine (Go)       │──▶ Local AI model
          │  • Arabic / English NLP      │
          │  • Command routing           │
          └───────┬──────────────┬───────┘
                  ▼              ▼
        ┌──────────────┐  ┌──────────────────┐
        │ Speech Synth │  │ Automation &     │
        │ (Piper)      │  │ Browser Ext.     │
        └──────────────┘  └──────────────────┘
```

## 🧪 Testing

```bash
go test ./...
```

## 🛣️ Roadmap

- [ ] Improve Arabic dialect understanding
- [ ] Wake-word detection
- [ ] Plugin system for custom automations
- [ ] Richer browser actions (tab control, page summarization)
- [ ] Configuration file and CLI flags
- [ ] Cross-platform support

## 🤝 Contributing

Contributions, ideas, and bug reports are welcome.

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Commit your changes: `git commit -m "Add my feature"`
4. Push to your branch: `git push origin feature/my-feature`
5. Open a Pull Request

## 🔐 Privacy

Jave Engine is designed to run locally. By default, no conversation data, audio, or browsing activity is sent to external servers.

## 📄 License

No license has been specified yet. Add a `LICENSE` file (e.g. MIT) to define how others may use this project.

## 👤 Author

**Youssef Elkahky** — [@YoussefElkahky](https://github.com/YoussefElkahky)

---

<div align="center">

*Built with Go. Powered locally. Made for privacy.*

</div>
