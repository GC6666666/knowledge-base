# Knowledge Base

AI-powered personal knowledge base with automatic file classification, summarization, and search.

## Features

- **Auto-classification** — Images, videos, audio, and text are automatically organized by date and type
- **AI Summarization** — Files are processed by AI to generate summaries and extract insights
- **Semantic Search** — Find anything using natural language
- **Persistent Storage** — All data is versioned and backed up

## Architecture

```
data/
├── raw/          # Original files (unmodified)
├── classified/   # Auto-organized by date/type
└── processed/    # AI outputs (summaries, transcriptions)
```

## Tech Stack

- File watcher (inotify / fswatch)
- Claude API for AI processing
- SQLite / vector DB for index
- FastAPI for API layer
