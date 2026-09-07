# Project conventions

This is the PROJECT-ROOT CLAUDE.md. NestedClaudeMD must never return it: Claude Code re-injects
the project-root file from disk itself after compaction (Qompack.md §2.7), so restoring it a
second time would spend rehydration budget on context the host has already put back.
