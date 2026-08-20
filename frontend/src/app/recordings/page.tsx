"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import {
  createLibraryCategory,
  deleteLibraryCategory,
  deleteLibraryFile,
  fetchLibraryBlob,
  fetchLibraryCategories,
  fetchLibraryFiles,
  fetchRecordingsPath,
  moveLibraryFile,
  renameLibraryCategory,
  setRecordingsPath,
  type LibraryCategory,
  type LibraryFile,
} from "@/lib/api";

export default function LibraryPage() {
  const [categories, setCategories] = useState<LibraryCategory[]>([]);
  const [files, setFiles] = useState<LibraryFile[]>([]);
  const [selectedCat, setSelectedCat] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [newCat, setNewCat] = useState("");
  const [storagePath, setStoragePath] = useState("");
  const [pathDraft, setPathDraft] = useState("");
  const [pathBusy, setPathBusy] = useState(false);
  const [playerURL, setPlayerURL] = useState<string | null>(null);
  const [playingKey, setPlayingKey] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [cats, list, path] = await Promise.all([
        fetchLibraryCategories(),
        fetchLibraryFiles(selectedCat || undefined),
        fetchRecordingsPath(),
      ]);
      setCategories(cats);
      setFiles(list);
      setStoragePath((prev) => {
        setPathDraft((draft) => (draft === "" || draft === prev ? path : draft));
        return path;
      });
      setError(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, [selectedCat]);

  const savePath = async () => {
    setPathBusy(true);
    try {
      const path = await setRecordingsPath(pathDraft.trim());
      setStoragePath(path);
      setPathDraft(path);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setPathBusy(false);
    }
  };

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => {
      clearInterval(t);
      if (playerURL) URL.revokeObjectURL(playerURL);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load]);

  const fileKey = (f: LibraryFile) => `${f.category}/${f.name}`;

  const playFile = async (f: LibraryFile) => {
    const key = fileKey(f);
    setBusyKey(key);
    try {
      const blob = await fetchLibraryBlob(f.category, f.name);
      if (playerURL) URL.revokeObjectURL(playerURL);
      setPlayerURL(URL.createObjectURL(blob));
      setPlayingKey(key);
    } finally {
      setBusyKey(null);
    }
  };

  const downloadFile = async (f: LibraryFile) => {
    const key = fileKey(f);
    setBusyKey(key);
    try {
      const blob = await fetchLibraryBlob(f.category, f.name);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = f.name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } finally {
      setBusyKey(null);
    }
  };

  const removeFile = async (f: LibraryFile) => {
    if (!window.confirm(`Delete "${f.name}" from ${f.category}?`)) return;
    const key = fileKey(f);
    setBusyKey(key);
    try {
      await deleteLibraryFile(f.category, f.name);
      if (playingKey === key) {
        if (playerURL) URL.revokeObjectURL(playerURL);
        setPlayerURL(null);
        setPlayingKey(null);
      }
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusyKey(null);
    }
  };

  const moveFile = async (f: LibraryFile, toCategory: string) => {
    if (!toCategory || toCategory === f.category) return;
    const key = fileKey(f);
    setBusyKey(key);
    try {
      await moveLibraryFile(f.category, toCategory, f.name);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusyKey(null);
    }
  };

  const addCategory = async () => {
    const name = newCat.trim();
    if (!name) return;
    try {
      await createLibraryCategory(name);
      setNewCat("");
      await load();
    } catch (e) {
      setError(String(e));
    }
  };

  const renameCategory = async (name: string) => {
    const next = window.prompt("Rename category", name);
    if (!next || next.trim() === name) return;
    try {
      await renameLibraryCategory(name, next.trim());
      if (selectedCat === name) setSelectedCat(next.trim().replace(/\s+/g, "_"));
      await load();
    } catch (e) {
      setError(String(e));
    }
  };

  const removeCategory = async (name: string) => {
    if (!window.confirm(`Delete empty category "${name}"?`)) return;
    try {
      await deleteLibraryCategory(name);
      if (selectedCat === name) setSelectedCat("");
      await load();
    } catch (e) {
      setError(String(e));
    }
  };

  return (
    <div className="recordings-page">
      <div className="recordings-header">
        <div className="library-header-left">
          <Link href="/" className="library-back">
            ← Grid
          </Link>
          <h1>Recordings library</h1>
        </div>
        <button className="global-rec-btn" onClick={load} disabled={loading}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      <div className="library-path-bar">
        <label className="library-path-label" htmlFor="recordings-path">
          Storage path
        </label>
        <input
          id="recordings-path"
          className="library-path-input"
          value={pathDraft}
          onChange={(e) => setPathDraft(e.target.value)}
          placeholder="/data/recordings"
          disabled={pathBusy}
          title="Absolute path on the capture host where category folders are created"
        />
        <button
          type="button"
          className="badge files-btn"
          onClick={savePath}
          disabled={pathBusy || !pathDraft.trim() || pathDraft.trim() === storagePath}
        >
          {pathBusy ? "…" : "Save"}
        </button>
      </div>

      {error && <div className="error-message">{error}</div>}

      <div className="library-layout">
        <aside className="library-sidebar">
          <div className="library-sidebar-title">Categories</div>
          <button
            type="button"
            className={`library-cat ${selectedCat === "" ? "active" : ""}`}
            onClick={() => setSelectedCat("")}
          >
            All
          </button>
          {categories.map((c) => (
            <div key={c.name} className="library-cat-row">
              <button
                type="button"
                className={`library-cat ${selectedCat === c.name ? "active" : ""}`}
                onClick={() => setSelectedCat(c.name)}
              >
                <span>{c.name === "_unsorted" ? "Unsorted" : c.name}</span>
                <span className="library-cat-count">{c.file_count}</span>
              </button>
              {c.name !== "_unsorted" && (
                <div className="library-cat-ops">
                  <button type="button" title="Rename" onClick={() => renameCategory(c.name)}>
                    ✎
                  </button>
                  <button type="button" title="Delete if empty" onClick={() => removeCategory(c.name)}>
                    ×
                  </button>
                </div>
              )}
            </div>
          ))}
          <div className="library-new-cat">
            <input
              value={newCat}
              onChange={(e) => setNewCat(e.target.value)}
              placeholder="New category"
              onKeyDown={(e) => {
                if (e.key === "Enter") addCategory();
              }}
            />
            <button type="button" className="badge files-btn" onClick={addCategory}>
              Add
            </button>
          </div>
        </aside>

        <div className="recordings-list">
          {files.length === 0 ? (
            <div className="files-empty">No recordings found.</div>
          ) : (
            files.map((f) => {
              const key = fileKey(f);
              return (
                <div key={key} className="recording-row">
                  <div className="recording-meta">
                    <div className="recording-name">{f.name}</div>
                    <div className="recording-sub">
                      {f.category === "_unsorted" ? "Unsorted" : f.category}
                      {" · "}
                      {Math.round(f.size / 1024 / 1024)} MB
                    </div>
                  </div>
                  <div className="recording-actions">
                    <select
                      className="encode-preset-select"
                      value=""
                      disabled={busyKey === key}
                      onChange={(e) => {
                        const v = e.target.value;
                        e.target.value = "";
                        if (v) moveFile(f, v);
                      }}
                      title="Move to category"
                    >
                      <option value="">Move…</option>
                      {categories
                        .filter((c) => c.name !== f.category)
                        .map((c) => (
                          <option key={c.name} value={c.name}>
                            {c.name === "_unsorted" ? "Unsorted" : c.name}
                          </option>
                        ))}
                    </select>
                    <button
                      className="badge files-btn"
                      onClick={() => playFile(f)}
                      disabled={busyKey === key}
                    >
                      {busyKey === key ? "…" : "Play"}
                    </button>
                    <button className="badge" onClick={() => downloadFile(f)} disabled={busyKey === key}>
                      Download
                    </button>
                    <button className="badge delete-btn" onClick={() => removeFile(f)} disabled={busyKey === key}>
                      Delete
                    </button>
                  </div>
                </div>
              );
            })
          )}
        </div>

        <div className="recordings-player-wrap">
          {playerURL ? (
            <video className="recordings-player" controls src={playerURL} />
          ) : (
            <div className="files-empty">Select a file to play.</div>
          )}
        </div>
      </div>
    </div>
  );
}
