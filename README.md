# 🦉 OFileDiff

<p align="center">
  <img src="https://img.shields.io/badge/Platform-Linux-0a0c10?style=flat-square"/>
  <img src="https://img.shields.io/badge/Category-Core%20/%20Utilities-blue?style=flat-square"/>
  <img src="https://img.shields.io/badge/License-MIT-green?style=flat-square"/>
  <img src="https://img.shields.io/badge/Part%20of-OwlSec%20Toolkit-7b5ea7?style=flat-square"/>
  <img src="https://img.shields.io/badge/Version-1.0-cyan?style=flat-square"/>
</p>

> **OFileDiff** — Text, binary & directory diff with unified patch + apply ║

---

> ⚠️ **AUTHORISED USE ONLY** — Use only on systems you own or have explicit written permission to test. Unauthorised use is illegal.

---

## 📌 Overview

OFileDiff — Text, binary & directory diff with unified patch + apply. Text · Binary · Directory Diff & Unified Patch Linear-space Myers diff with GNU-compatible unified output (including "\ No newline at end of file"), whitespace / case / blank-line options, word-level highlighting, side-by-side view, diffstat, JSON and self-contained HTML reports, binary diff (prefix/suffix alignment + patched byte ranges), recursive directory compare with a -ruN style patch, and a built-in patch applier (offset search, -p N, reverse, dry run, path-traversal guard).

---

## ✨ Features

- Diff text, binary files and whole directories
- Produce unified patches and apply them safely
- Highlight structural vs content changes
- Directory walk with exclude-friendly workflows
- Useful for release review and forensic file comparison
- CLI-first with optional interactive views

---

## ⚙️ Requirements

- **Linux**
- **Go 1.21+** (stdlib only; `CGO_ENABLED=0`)

---

## 🚀 Usage

```bash
go build -ldflags="-s -w" -o OFileDiff OFileDiff.go
./OFileDiff --help
./OFileDiff --left a.txt --right b.txt
./OFileDiff --left old/ --right new/ --unified
./OFileDiff --patch changes.diff --apply
```

---

## 📦 Part of OwlSec Toolkit

This tool is part of the **OwlSec** suite — a collection of 300+ security and privacy tools.

🔗 [owlsec.org](https://owlsec.org)

---

## ©️ License

MIT License — © Khaled S. Haddad
