# 🚀 SGo-Scraper

A concurrent, rate-limited, and database-tracked SuicideGirls media scraper written in pure Go. It supports downloading photosets, candids, videos, blogs, and group thread comment attachments, featuring a clean console interface and intelligent SQLite history tracking.

---

## ✨ Key Features

*   **💾 Smart SQLite History Tracking**: Keeps track of downloaded items inside a CGO-free SQLite database. Once files are downloaded, you can safely move them out of the downloads directory—the scraper checks the database to skip previously downloaded items on subsequent sweeps.
*   **📂 Structured Folder Layout**: Organizes all model downloads under a unified directory structure:
    `downloadsDir/<modelName>/{photos, candids, videos}`.
*   **📊 Dynamic Terminal Progress Bar**: Displays real-time download status, item counts, and total downloaded data (in MB) with clean error formatting.
*   **⏳ Rate-Limited Concurrency**: Staggers downloads with 5 concurrent workers spaced out by a configurable delay (`DOWNLOAD_DELAY_MS`, default 2000ms) to prevent connection resets and rate-limiting blocks.
*   **🔍 Model Profile Sweeper**: Supplying a model's base URL (e.g., `https://www.suicidegirls.com/girls/modelname/`) will automatically check for and download their photosets, candids, videos, and blogs in sequence.
*   **🛠️ Windows Path Sanitizer**: Automatically cleans up and collapses tabs, newlines (`\n`), carriage returns (`\r`), and multiple spaces in post titles to guarantee valid directory and filenames on Windows.
*   **🔗 Group Thread Comment Downloader**: Downloads image attachments from group threads, grouping attachments comment-by-comment.

---

## 📁 Download Directory Structure

All downloaded media is cleanly sorted as follows:

```text
downloadsDir/
├── <modelName>/
│   ├── photos/
│   │   └── <modelName> - <albumName>/
│   │       ├── <albumID> - 0001.jpg
│   │       └── <albumName>.zip          # Optional zipped finalization
│   ├── candids/
│   │   ├── <postID> - <singleImagePost> - 0001.jpg
│   │   └── <postID> - <multiImagePost>/
│   │       ├── <postID> - <multiImagePost> - 0001.jpg
│   │       └── <postID> - <multiImagePost> - 0002.jpg
│   └── videos/
│       └── <videoID> - <videoTitle>.mp4
└── groups/
    └── <groupName>/
        └── <threadID> - <threadTitle>/
            ├── <commentID> - <username> - <snippet> - 0001.jpg
            └── <commentID> - <username> - <snippet> - 0002.jpg
```

---

## 🗄️ SQLite History Database

To ensure you can organize and move your downloads without the scraper trying to download them again, the scraper writes records into a `modelsdb` folder located in the **same directory as the scraper executable**:

*   **Model History**: `<exeDir>/modelsdb/<modelName>.db`
*   **Groups History**: `<exeDir>/modelsdb/groups.db`

> [!NOTE]
> **Legacy Migration**: If the scraper finds files already on disk that are not registered in the database, it will skip downloading them and automatically write records to the SQLite DB to backfill your history!

Items already in the database are skipped **without loading their pages**: the item's ID and model are read from its URL. This makes re-running a big profile much faster and sends far fewer requests.

An item is only recorded once **every** file in it downloaded successfully. Images and videos are written to a `.part` file first and renamed when complete, so an interrupted run never leaves a file that looks finished. Items that fail are listed at the end of the run and retried next time.

Brief network problems (timeouts, dropped connections, 5xx errors) are retried up to 2 more times before an item counts as failed. Images are saved with the extension that matches what the server sends (`.jpg`, `.png`, `.webp`, …).

Before each run the scraper checks that your cookies still log in, and stops with a clear message if they've expired.

### Exit codes

| Code | Meaning | Watcher does |
|------|---------|--------------|
| `0` | Everything downloaded (or was already downloaded). | Deletes the queued file |
| `3` | The run finished, but some items failed; they are listed at the end. | Moves it to `Incoming\Failed` |
| `4` | The login check failed: the cookies in `.env` have probably expired. | Keeps it, pauses 5 minutes |
| `5` | The URL isn't a SuicideGirls page the scraper handles. | Moves it to `Incoming\Failed` |
| `429` | The site rate-limited the scraper. | Keeps it, pauses 5 minutes |
| other | The run stopped early. | Keeps it, pauses 30 seconds |

---

## ⚙️ Configuration (`.env`)

Create a `.env` file in the root directory:

```env
DOWNLOADSDIR="d:\Users\xxx\Downloads\SGo-Scraper-master-test\downloads"
SESSIONIDTOKEN="your_suicidegirls_sessionid_cookie"
SGCSRFTOKEN="your_sgcsrftoken_cookie"
RSCI_VID="your_rscivid_cookie"
DOWNLOAD_DELAY_MS=2000
USER_AGENT="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
```

Optional settings:

| Setting | Default | What it does |
|---------|---------|--------------|
| `VIDEO_TIMEOUT_MIN` | `180` | The longest one video download may take, in minutes. A video that gets no new data for 2 minutes is stopped sooner. |
| `LOGIN_CHECK` | on | Set to `off` to skip the login check at the start of each run. |
| `COLOR` | on | Set to `off` for plain, uncolored output. Setting `NO_COLOR` to anything does the same. |
| `SHOW_SKIPS` | on | Set to `off` to hide the `[skip]` lines for items already downloaded. They're still counted in the summary. |

Each run ends with a summary line, e.g. `Done in 2m14s — Downloaded 3 · Skipped 151 · Failed 0 · 4.37 MB`.

### Output colors

| Color | Meaning |
|-------|---------|
| Cyan, bold | Section headings (`Found 12 Photosets`) and the model name |
| White, bold | The item being downloaded |
| Blue | `[03/40]` counters |
| Cyan → green bar | Download progress; the bar turns green when the item is complete |
| Green | Success: `Logged in as …`, `Saved …`, `Finished Downloading…` |
| Yellow | Retries and warnings |
| Red | Errors, failures and the end-of-run failure list |
| Grey | Skipped items, folder paths and group-thread details |

Color turns off automatically when the output is redirected to a file.

### Cookie Acquisition:
1. Log in to the SuicideGirls website in your browser.
2. Open Developer Tools (F12) -> Application -> Cookies.
3. Copy the values for `sessid` (map to `SESSIONIDTOKEN`), `sgcsrftoken`, and `rscivid`.

---

## 🚀 How to Run

### Direct Command Line
Ensure you have Go 1.25+ installed. Run the following command in the project directory:

```powershell
# Build the binary
go build -o SGo-Scraper-master.exe

# Download a photoset (with optional zip finalization "-z")
.\SGo-Scraper-master.exe <albumURL> [-z]

# Download a model's entire profile (photosets, candids, videos, blogs)
.\SGo-Scraper-master.exe https://www.suicidegirls.com/girls/<modelName>/
```

### Automation Watcher (Windows)
The repository includes a Powershell directory watcher.
1. Run `StartWatcher.bat` (which triggers `Watcher.ps1`).
2. Drag and drop any `.url` shortcut or `.txt` file with a URL into the `Incoming` directory. A `.txt` file's first line starting with `http://` or `https://` is used.
3. The watcher will parse the URL, run the scraper, and clear the file when finished.
4. If some items in a run fail (exit code `3`), the URL isn't recognised (exit code `5`), or the file has no URL in it, the file is moved to `Incoming\Failed`. Drop it back into `Incoming` to retry.

### Tests
Run the unit tests for the page parsers and download logic with:

```powershell
go test ./...
```

---

## 💻 Tech Stack
*   **Language**: Go 1.25+ (utilizes standard Goroutines and WaitGroups)
*   **Libraries**:
    *   `github.com/joho/godotenv` - `.env` config loader
    *   `golang.org/x/net/html` - HTML token parsing
    *   `modernc.org/sqlite` - 100% CGO-free pure Go SQLite driver (no GCC compilation requirements)
*   **Video Handler**: `ffmpeg` (must be in your system `%PATH%` to download stream segments into `.mp4` format)
