### fix(headroom): resolve binary symlink and fallback to importlib.metadata for extras detection

When Headroom is installed via `uv tool` or `pipx`:
- The CLI binary in `~/.local/bin/headroom` is a symlink pointing to an isolated virtual environment (`~/.local/share/uv/tools/headroom-ai/bin/headroom`), where the virtualenv's Python interpreter lives. Upstream did not resolve symlinks, causing `pythonCandidates` to miss the virtualenv Python and fallback to the system Python.
- Modern `uv` virtual environments do not include `pip` by default (`No module named pip`), causing `python -m pip list` to fail and reporting extras (`[code]`, `[ml]`) as not installed.

Fixes applied:
- Resolve binary symlinks in `pythonCandidates` using `filepath.EvalSymlinks` to discover the virtualenv Python.
- Check `import headroom` as an interpreter verification fallback in `FindPython310`.
- Fallback to Python standard library `importlib.metadata` in `GetInstalledHeadroomExtras` when `pip` is not installed.
