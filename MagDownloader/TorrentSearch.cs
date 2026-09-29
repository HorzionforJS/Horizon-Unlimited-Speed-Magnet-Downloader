using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Text;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 种子搜索结果 ============
    internal class TorrentResult
    {
        public string Name = "";

        // 服务端回填的中文片名（技术标签保留英文，如「流浪地球  2019 1080p BluRay x264」）。
        // 仅用于阅读方便：种子名里的英文原名才是查证资源真假的依据，所以两者都保留、
        // 分列展示，而不是拿中文名把原名替换掉。
        public string NameZh = "";
        public string InfoHash = "";
        public string Category = "";
        public long Size = 0;
        public int Seeders = 0;
        public int Leechers = 0;

        // Added 是种子发布时间（Unix 秒），0 表示服务端没给。
        // 单看做种数会永远偏向老种子（积累时间长），要判断「是不是新片」得看它。
        public long Added = 0;

        public string AddedLabel
        {
            get
            {
                if (Added <= 0) return "-";
                DateTime t;
                try { t = DateTimeOffset.FromUnixTimeSeconds(Added).LocalDateTime; }
                catch { return "-"; }
                TimeSpan age = DateTime.Now - t;
                if (age.TotalDays < 1) return "今天";
                if (age.TotalDays < 2) return "昨天";
                if (age.TotalDays < 30) return (int)age.TotalDays + " 天前";
                if (age.TotalDays < 365) return (int)(age.TotalDays / 30) + " 个月前";
                return t.ToString("yyyy-MM-dd");
            }
        }

        // Year 从原始文件名里抠出年份，用于确认「这是哪一年的版本」。
        public string Year
        {
            get
            {
                System.Text.RegularExpressions.Match m =
                    System.Text.RegularExpressions.Regex.Match(Name, @"[.\s(\[]((?:19|20)\d{2})[.\s)\]]");
                return m.Success ? m.Groups[1].Value : "";
            }
        }

        // 列表「中文名」列要显示的内容：有译文用它，没有就退回原名。
        public string DisplayName
        {
            get { return NameZh.Length > 0 ? NameZh : Name; }
        }

        // 「搜索磁力」的中文片名会先翻译再检索，这里回填实际检索词与提示，
        // 让界面能告诉用户「搜的其实是这个」，避免把无关结果误当成目标影片。
        public string SearchTerm = "";
        public string Hint = "";

        public string CatLabel
        {
            get
            {
                switch (Category)
                {
                    case "200": return "视频";
                    case "201": case "202": case "207": case "209": return "电影";
                    case "205": case "208": return "剧集";
                    case "211": return "4K电影";
                    case "212": return "4K剧集";
                    case "100": case "101": case "102": case "103": case "104": return "音乐";
                    case "300": case "301": case "302": case "303": return "软件";
                    case "400": case "401": case "402": case "403": case "404": case "405": case "406": return "游戏";
                    case "600": case "601": case "602": case "603": case "604": case "605": return "其他";
                    default:
                        if (Category.StartsWith("5", StringComparison.Ordinal)) return "成人";
                        return "其他";
                }
            }
        }

        public string Magnet
        {
            get
            {
                string tr = "udp://tracker.opentrackr.org:1337/announce&tr=" +
                    "udp://open.demonii.com:1337/announce&tr=" +
                    "udp://tracker.torrent.eu.org:451/announce&tr=" +
                    "udp://tracker.openbittorrent.com:6969/announce&tr=" +
                    "udp://exodus.desync.com:6969/announce&tr=" +
                    "http://tracker.openbittorrent.com:80/announce&tr=" +
                    "https://tracker.tamersunion.org/announce";
                return "magnet:?xt=urn:btih:" + InfoHash.ToLowerInvariant() +
                    "&dn=" + Uri.EscapeDataString(Name) + "&tr=" + tr;
            }
        }

        public static string FmtSize(long b)
        {
            if (b <= 0) return "未知";
            double v = b;
            string[] u = new string[] { "B", "KB", "MB", "GB", "TB" };
            int i = 0;
            while (v >= 1024 && i < u.Length - 1) { v /= 1024; i++; }
            return v.ToString(i == 0 ? "0" : "0.0") + " " + u[i];
        }
    }

    // ============ 种子搜索引擎（The Pirate Bay 公开 API，apibay.org） ============
    internal static class TorrentSearch
    {
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        static TorrentSearch()
        {
            try { ServicePointManager.SecurityProtocol = SecurityProtocolType.Tls12; } catch { }
        }

        // 分类下拉框 -> TPB 分类 id
        public static string CatIdFor(int sel)
        {
            switch (sel)
            {
                case 1: return "207";  // 电影
                case 2: return "208";  // 剧集
                case 3: return "211";  // 4K电影
                case 4: return "100";  // 音乐
                case 5: return "400";  // 游戏
                case 6: return "300";  // 软件
                default: return "0";   // 全部
            }
        }

        // 服务端搜索总预算 20s（含中文片名回显），客户端必须等得比它久，
        // 否则会出现「服务端还在认真干活、客户端已经放弃」的白等，用户看到的是搜索失败。
        private const int CloudTimeoutMs = 30000;

        // SortMode：0=综合（服务端默认顺序）1=做种最多 2=最新发布 3=体积从大到小
        public static int SortMode = 0;

        // SortResults 按用户选的排序方式重排。
        // 「综合」保持服务端顺序不动 —— 那套顺序（热度分档 + 同档看新旧）已经
        // 比单纯的做种数排序更符合「找片」的直觉。
        public static void SortResults(List<TorrentResult> list)
        {
            if (list == null || list.Count < 2) return;
            switch (SortMode)
            {
                case 1:
                    list.Sort(delegate(TorrentResult a, TorrentResult b) { return b.Seeders.CompareTo(a.Seeders); });
                    break;
                case 2:
                    list.Sort(delegate(TorrentResult a, TorrentResult b) { return b.Added.CompareTo(a.Added); });
                    break;
                case 3:
                    list.Sort(delegate(TorrentResult a, TorrentResult b) { return b.Size.CompareTo(a.Size); });
                    break;
            }
        }

        // 请求多少条结果。每条都要在服务端翻译片名，越多越慢，
        // 120 条已经远超界面上能看得完的量。
        private const int SearchLimit = 120;

        // 上次搜索的过程信息（实际检索词 / 提示），供界面展示。
        public static string LastSearchTerm = "";
        public static string LastHint = "";
        public static bool LastUsedCloud = false;

        public static List<TorrentResult> Search(string q, string cat)
        {
            LastSearchTerm = "";
            LastHint = "";
            LastUsedCloud = false;
            List<TorrentResult> list = new List<TorrentResult>();
            if (string.IsNullOrWhiteSpace(q)) return list;

            // 服务器地址从配置文件读（登录页「服务器设置」写入），必须在判断前完成。
            CloudConfig.Load();

            // 优先走云端：服务端会把中文片名翻译成英文再检索，并过滤掉无关结果。
            // 本地直连 apibay 时中文关键词会返回满屏无关热门种子（实测搜「云雀叫天录」
            // 返回的是《蜘蛛侠》），所以云端是中文检索的唯一可靠路径。
            list = SearchCloud(q, cat);
            if (list != null)
            {
                LastUsedCloud = true;
                // 这里刻意不再按做种数重排。
                // 服务端已经按「做种数分档 + 同档内新的优先」排好；客户端再按
                // 做种数排一次会把新片全压到后面——做种数天然偏向老种子。
                return list;
            }

            // 云端不可用时回退本地直连（英文关键词可用；中文会不准确，由界面提示）。
            list = SearchLocal(q, cat);
            if (list == null) list = new List<TorrentResult>();
            list.Sort(delegate(TorrentResult a, TorrentResult b) { return b.Seeders.CompareTo(a.Seeders); });
            if (HasCJK(q))
            {
                LastHint = CloudConfig.IsConfigured
                    ? "云端服务不可用，已改用本地检索。中文关键词在英文索引站点上结果可能不准确，请检查云端服务器地址后重试。"
                    : CloudConfig.NotConfigured + "，已改用本地检索；中文关键词在英文索引站点上结果可能不准确。";
            }
            else if (!CloudConfig.IsConfigured)
            {
                LastHint = CloudConfig.NotConfigured;
            }
            return list;
        }

        // 经服务端 /api/v1/search 检索（无需登录）；返回 null 表示云端不可用。
        private static List<TorrentResult> SearchCloud(string q, string cat)
        {
            if (!CloudConfig.IsConfigured) return null;
            try
            {
                // limit 不只是省流量：服务端要为每条结果额外翻译片名，
                // 返回 200 条比返回 40 条慢得多，而用户根本不会翻到底。
                string url = CloudConfig.ServerUrl + "/api/v1/search?q=" + Uri.EscapeDataString(q.Trim()) +
                    "&cat=" + Uri.EscapeDataString(string.IsNullOrEmpty(cat) ? "0" : cat) +
                    "&limit=" + SearchLimit;
                string jsonText = Get(url, CloudTimeoutMs);
                if (string.IsNullOrEmpty(jsonText)) return null;

                Dictionary<string, object> d = Json.DeserializeObject(jsonText) as Dictionary<string, object>;
                if (d == null) return null;

                LastSearchTerm = S(d.ContainsKey("search_term") ? d["search_term"] : null);
                LastHint = S(d.ContainsKey("hint") ? d["hint"] : null);

                List<TorrentResult> list = new List<TorrentResult>();
                object[] arr = d.ContainsKey("results") ? d["results"] as object[] : null;
                if (arr != null)
                {
                    foreach (object o in arr)
                    {
                        TorrentResult r = Parse(o as Dictionary<string, object>);
                        if (r != null) list.Add(r);
                    }
                }
                return list;
            }
            catch (Exception ex)
            {
                Logger.App("云端检索失败，回退本地：" + ex.Message);
                return null;
            }
        }

        // 本地直连 apibay（云端不可用时的兜底）。
        private static List<TorrentResult> SearchLocal(string q, string cat)
        {
            try
            {
                string url = "https://apibay.org/q.php?q=" + Uri.EscapeDataString(q.Trim()) +
                    "&cat=" + (string.IsNullOrEmpty(cat) ? "0" : cat);
                string jsonText = Get(url, 12000);
                if (string.IsNullOrEmpty(jsonText)) return null;

                object parsed = Json.DeserializeObject(jsonText);
                object[] arr = parsed as object[];
                if (arr == null) return null;
                List<TorrentResult> list = new List<TorrentResult>();
                foreach (object o in arr)
                {
                    TorrentResult r = Parse(o as Dictionary<string, object>);
                    if (r != null) list.Add(r);
                }
                return list;
            }
            catch { return null; }
        }

        private static TorrentResult Parse(Dictionary<string, object> d)
        {
            if (d == null) return null;
            try
            {
                TorrentResult r = new TorrentResult();
                r.Name = S(d.ContainsKey("name") ? d["name"] : null);
                r.NameZh = S(d.ContainsKey("name_zh") ? d["name_zh"] : null);
                r.InfoHash = S(d.ContainsKey("info_hash") ? d["info_hash"] : null);
                r.Category = S(d.ContainsKey("category") ? d["category"] : null);
                r.Size = L(d.ContainsKey("size") ? d["size"] : null);
                r.Seeders = (int)L(d.ContainsKey("seeders") ? d["seeders"] : null);
                r.Leechers = (int)L(d.ContainsKey("leechers") ? d["leechers"] : null);
                r.Added = L(d.ContainsKey("added") ? d["added"] : null);
                if (r.Name.Length == 0 || r.InfoHash.Length == 0) return null;
                return r;
            }
            catch { return null; }
        }

        // 是否含中日韩文字（用于判断是否需要走云端翻译检索）。
        public static bool HasCJK(string s)
        {
            if (string.IsNullOrEmpty(s)) return false;
            foreach (char ch in s)
            {
                if ((ch >= 0x4E00 && ch <= 0x9FFF) || (ch >= 0x3400 && ch <= 0x4DBF) || (ch >= 0xF900 && ch <= 0xFAFF))
                    return true;
            }
            return false;
        }
        private static string Get(string url) { return Get(url, 12000); }

        private static string Get(string url, int timeoutMs)
        {
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url);
                req.Method = "GET";
                req.Timeout = timeoutMs;
                req.ReadWriteTimeout = timeoutMs;
                req.UserAgent = "MagDownloader/" + AppVersion.Number;
                req.Accept = "application/json";
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                    return sr.ReadToEnd();
            }
            catch { return null; }
        }

        private static string S(object o) { return o == null ? "" : o.ToString(); }

        private static long L(object o)
        {
            if (o == null) return 0;
            if (o is long) return (long)o;
            if (o is int) return (int)o;
            if (o is double) return (long)(double)o;
            if (o is decimal) return (long)(decimal)o;
            long v; long.TryParse(o.ToString(), out v); return v;
        }
    }
}