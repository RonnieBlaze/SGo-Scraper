# 🚀 SGo-Scraper

A concurrent, rate-limited, and database-tracked SuicideGirls media scraper written in pure Go. It supports downloading photosets, candids, videos, blogs, and group thread comment attachments, featuring a clean console interface and intelligent SQLite history tracking.

---

## ✨ Key Features

*   **💾 Smart SQLite History Tracking**: Keeps track of downloaded items inside a CGO-free SQLite database. Once files are downloaded, you can safely move them out of the downloads directory—the scraper checks the database to skip previously downloaded items on subsequent sweeps.
*   **📂 Structured Folder Layout**: Organizes all model downloads under a unified directory structure:
    `downloadsDir/<modelName>/{photos, candids, videos}`.
*   **📊 Dynamic Terminal Progress Bar**: Displays real-time download status, item counts, and total downloaded data (in MB) with clean error formatting.
*   **⏳ Rate-Limited Concurrency**: Staggers downloads with exactly 5 concurrent workers spaced out by a 500ms delay to prevent connection resets and rate-limiting blocks.
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

---

## ⚙️ Configuration (`.env`)

Create a `.env` file in the root directory:

```env
DOWNLOADSDIR="d:\Users\xxx\Downloads\SGo-Scraper-master-test\downloads"
SESSIONIDTOKEN="your_suicidegirls_sessionid_cookie"
SGCSRFTOKEN="your_sgcsrftoken_cookie"
RSCIVID="your_rscivid_cookie"
```

### Cookie Acquisition:
1. Log in to the SuicideGirls website in your browser.
2. Open Developer Tools (F12) -> Application -> Cookies.
3. Copy the values for `sessid` (map to `SESSIONIDTOKEN`), `sgcsrftoken`, and `rscivid`.

---

## 🚀 How to Run

### Direct Command Line
Ensure you have Go 1.21+ installed. Run the following command in the project directory:

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
2. Drag and drop any `.url` shortcut or text file with a URL into the `Incoming` directory.
3. The watcher will parse the URL, run the scraper, and clear the file when finished.

---

## 💻 Tech Stack
*   **Language**: Go 1.21+ (utilizes standard Goroutines and WaitGroups)
*   **Libraries**:
    *   `github.com/joho/godotenv` - `.env` config loader
    *   `golang.org/x/net/html` - HTML token parsing
    *   `modernc.org/sqlite` - 100% CGO-free pure Go SQLite driver (no GCC compilation requirements)
*   **Video Handler**: `ffmpeg` (must be in your system `%PATH%` to download stream segments into `.mp4` format)
