using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 下载历史条目 ============
    internal class HistoryEntry
    {
        public string Name;        // 文件名 / 任务名
        public string Source;      // 磁力 / HTTP 链接
        public string Dir;         // 保存目录
        public string FilePath;    // 主文件（或目录）完整路径，可直接打开
        public long Size;          // 总大小（字节）
        public string Status;      // complete / error
        public string Time;        // 结束时间 yyyy-MM-dd HH:mm:ss
    }

    // ============ 下载历史（JSON 持久化，重启后仍可查看） ============
    internal static class HistoryStore
    {
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();
        private static List<HistoryEntry> _cache;

        private static string File { get { return Path.Combine(AppPaths.Data, "history.json"); } }

        public static List<HistoryEntry> All()
        {
            if (_cache != null) return _cache;
            _cache = new List<HistoryEntry>();
            try
            {
                if (System.IO.File.Exists(File))
                {
                    string s = System.IO.File.ReadAllText(File, Encoding.UTF8);
                    if (!string.IsNullOrWhiteSpace(s))
                        _cache = Json.Deserialize<List<HistoryEntry>>(s) ?? new List<HistoryEntry>();
                }
            }
            catch { _cache = new List<HistoryEntry>(); }
            return _cache;
        }

        public static void Add(HistoryEntry e)
        {
            All();
            // 去重：同名同路径不重复，最新排在最前
            for (int i = _cache.Count - 1; i >= 0; i--)
            {
                if (_cache[i].Name == e.Name && _cache[i].FilePath == e.FilePath)
                    _cache.RemoveAt(i);
            }
            _cache.Insert(0, e);
            while (_cache.Count > 500) _cache.RemoveAt(_cache.Count - 1);
            Save();
        }

        public static void Save()
        {
            try
            {
                AppPaths.Ensure();
                System.IO.File.WriteAllText(File, Json.Serialize(_cache ?? new List<HistoryEntry>()), Encoding.UTF8);
            }
            catch { }
        }
    }
}