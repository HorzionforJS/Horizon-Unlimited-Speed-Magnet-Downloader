﻿using System;
using System.Collections.Generic;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Globalization;
using System.IO;
using System.Net;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 电影天堂元数据（封面 / 名称 / 简介） ============
    //
    // 数据全部来自本项目的云端服务 /api/v1/dytt/*，客户端不直连内容源：
    // 内容源对高频请求直接返回 HTTP 400，只有服务端做了限速、重试与缓存。

    // 列表页 / 搜索页的一张卡片
    internal class MovieSummary
    {
        public long Id;
        public string Title = "";
        public string Url = "";
        public string Poster = "";
        public double Score;
        public string Status = "";
        public string Category = "";
        public List<string> Tags = new List<string>();
        public string UpdatedAt = "";

        // 海报墙用：没有封面时显示片名首字，避免卡片一片空白
        public string Initial
        {
            get { return string.IsNullOrEmpty(Title) ? "?" : Title.Substring(0, 1); }
        }
    }

    // 详情页的完整元数据
    internal class MovieDetail
    {
        public long Id;
        public string Title = "";
        public string Url = "";
        public string Poster = "";
        public string Summary = "";
        public double Score;
        public int Year;
        public string Category = "";
        public List<string> Actors = new List<string>();
        public List<string> Genres = new List<string>();
        public string Region = "";
        public string Language = "";
        public string FirstAired = "";
        public string UpdatedAt = "";
        public int EpisodeCount;
        public List<string> PlaySources = new List<string>();
        public List<MovieSummary> Related = new List<MovieSummary>();

        // 用于在磁力搜索引擎里检索的关键词。源站只提供在线播放、不含磁力链接，
        // 所以这里给的是精确检索词，而不是伪造的磁力地址。
        public List<string> MagnetQueries = new List<string>();
        public string SearchKeyword = "";

        public string Keyword
        {
            get { return string.IsNullOrEmpty(SearchKeyword) ? Title : SearchKeyword; }
        }

        // 一行式摘要：2026 · 国产 · 中国大陆 · 更新至第5集
        public string MetaLine()
        {
            List<string> parts = new List<string>();
            if (Year > 0) parts.Add(Year.ToString(CultureInfo.InvariantCulture));
            if (Category.Length > 0) parts.Add(Category);
            if (Region.Length > 0) parts.Add(Region);
            if (Language.Length > 0) parts.Add(Language);
            return string.Join(" · ", parts.ToArray());
        }
    }

    // 一页列表结果
    internal class MoviePage
    {
        public string Category = "";
        public int Page = 1;
        public int TotalPages;
        public bool HasNext;
        public int NextPage;
        public List<MovieSummary> Items = new List<MovieSummary>();

        // Stale=true 表示内容源暂时不可用，服务端拿上次成功抓取的片单顶上。
        // 必须区分出来：否则用户会把旧片单当成「今天没有更新」。
        public bool Stale;
        public string Hint = "";
    }

    // ============ 云端元数据客户端 ============
    internal static class DyttApi
    {
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        static DyttApi()
        {
            // 与下载引擎一致：老系统默认只开 TLS 1.0，访问 https 服务端会失败
            try { ServicePointManager.SecurityProtocol = SecurityProtocolType.Tls12; } catch { }
        }

        public static MoviePage Latest(int limit)
        {
            Dictionary<string, object> d = Get("/api/v1/dytt/latest?limit=" + limit.ToString(CultureInfo.InvariantCulture));
            MoviePage p = new MoviePage();
            p.Category = "latest";
            p.Items = ReadItems(d, "items");
            p.Stale = B(d, "stale");
            p.Hint = S(d, "hint");
            return p;
        }

        public static MoviePage ListPage(string category, int page)
        {
            Dictionary<string, object> d = Get("/api/v1/dytt/list?category=" + Uri.EscapeDataString(category ?? "movie") +
                "&page=" + page.ToString(CultureInfo.InvariantCulture));
            MoviePage p = new MoviePage();
            p.Category = S(d, "category");
            p.Page = (int)L(d, "page");
            p.TotalPages = (int)L(d, "total_pages");
            p.HasNext = B(d, "has_next");
            p.NextPage = (int)L(d, "next_page");
            p.Items = ReadItems(d, "items");
            p.Stale = B(d, "stale");
            p.Hint = S(d, "hint");
            return p;
        }

        public static MoviePage Search(string keyword)
        {
            Dictionary<string, object> d = Get("/api/v1/dytt/search?q=" + Uri.EscapeDataString((keyword ?? "").Trim()));
            MoviePage p = new MoviePage();
            p.Category = "search";
            p.Items = ReadItems(d, "items");
            p.Stale = B(d, "stale");
            p.Hint = S(d, "hint");
            return p;
        }

        public static MovieDetail Detail(string id)
        {
            Dictionary<string, object> d = Get("/api/v1/dytt/detail?id=" + Uri.EscapeDataString((id ?? "").Trim()));
            MovieDetail m = new MovieDetail();
            m.Id = L(d, "id");
            m.Title = S(d, "title");
            m.Url = S(d, "url");
            m.Poster = S(d, "poster");
            m.Summary = S(d, "summary");
            m.Score = (double)L(d, "score");
            m.Year = (int)L(d, "year");
            m.Category = S(d, "category");
            m.Region = S(d, "region");
            m.Language = S(d, "language");
            m.FirstAired = S(d, "first_aired");
            m.UpdatedAt = S(d, "updated_at");
            m.EpisodeCount = (int)L(d, "episode_count");
            m.Actors = ReadStrings(d, "actors");
            m.Genres = ReadStrings(d, "genres");
            m.MagnetQueries = ReadStrings(d, "magnet_queries");
            m.SearchKeyword = S(d, "search_keyword");
            m.Related = ReadItems(d, "related");

            object[] src = Arr(d, "play_sources");
            if (src != null)
                foreach (object o in src)
                {
                    Dictionary<string, object> s = o as Dictionary<string, object>;
                    if (s == null) continue;
                    string name = S(s, "name");
                    long cnt = L(s, "count");
                    if (name.Length == 0) continue;
                    m.PlaySources.Add(cnt > 0 ? name + "（" + cnt + " 集）" : name);
                }
            return m;
        }

        // ============ 底层 ============

        private static Dictionary<string, object> Get(string path)
        {
            CloudConfig.Load();
            if (!CloudConfig.IsConfigured)
                throw new Exception(CloudConfig.NotConfigured + "，影视库与云端检索暂不可用。");
            string url = CloudConfig.ServerUrl + path;
            string text = null;
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url);
                req.Method = "GET";
                // 必须大于服务端一次抓取的总时限（默认 12s，可用 HORIZON_DYTT_CALL_TIMEOUT 调）。
                // 否则服务端还在认真抓取、客户端已经放弃，用户只会看到「操作超时」，
                // 服务端准备好的那句「内容源暂时不可用」永远送不到。
                req.Timeout = 25000;
                req.ReadWriteTimeout = 25000;
                req.UserAgent = "MagDownloader/" + AppVersion.Number;
                req.Accept = "application/json";
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                    text = sr.ReadToEnd();
            }
            catch (WebException we)
            {
                // 服务端会用 {"error": "..."} 说明原因（未启用 / 被限流 / 参数错），
                // 这里把它翻出来，避免只抛一句“远程服务器返回错误”。
                HttpWebResponse resp = we.Response as HttpWebResponse;
                if (resp != null)
                {
                    try
                    {
                        using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                            text = sr.ReadToEnd();
                    }
                    catch { }
                    string msg = ErrorOf(text);
                    if (msg != null) throw new Exception(msg);
                    if (resp.StatusCode == HttpStatusCode.NotFound)
                        throw new Exception("云端服务缺少影视元数据接口，请先升级服务器再试");
                    throw new Exception("云端返回 HTTP " + (int)resp.StatusCode);
                }
                bool timeout = we.Status == WebExceptionStatus.Timeout;
                if (timeout)
                    throw new Exception("云端服务响应超时（" + CloudConfig.Describe() +
                        "）。若刚点开就失败，通常是内容源（电影天堂）暂时不可用；" +
                        "磁力搜索与下载不受影响。");
                throw new Exception("无法连接云端服务（" + CloudConfig.Describe() + "）：" + we.Message);
            }

            Dictionary<string, object> d = Json.DeserializeObject(text) as Dictionary<string, object>;
            if (d == null) throw new Exception("云端返回格式异常");
            if (d.ContainsKey("error")) throw new Exception(S(d, "error"));
            return d;
        }

        private static string ErrorOf(string json)
        {
            if (string.IsNullOrEmpty(json)) return null;
            try
            {
                Dictionary<string, object> d = Json.DeserializeObject(json) as Dictionary<string, object>;
                if (d != null && d.ContainsKey("error"))
                {
                    string e = S(d, "error");
                    if (e.Length > 0) return e;
                }
            }
            catch { }
            return null;
        }

        private static List<MovieSummary> ReadItems(Dictionary<string, object> d, string key)
        {
            List<MovieSummary> list = new List<MovieSummary>();
            object[] arr = Arr(d, key);
            if (arr == null) return list;
            foreach (object o in arr)
            {
                Dictionary<string, object> c = o as Dictionary<string, object>;
                if (c == null) continue;
                MovieSummary m = new MovieSummary();
                m.Id = L(c, "id");
                m.Title = S(c, "title");
                m.Url = S(c, "url");
                m.Poster = S(c, "poster");
                m.Score = (double)L(c, "score");
                m.Status = S(c, "status");
                m.Category = S(c, "category");
                m.UpdatedAt = S(c, "updated_at");
                m.Tags = ReadStrings(c, "tags");
                if (m.Title.Length == 0) continue;
                list.Add(m);
            }
            return list;
        }

        private static List<string> ReadStrings(Dictionary<string, object> d, string key)
        {
            List<string> list = new List<string>();
            object[] arr = Arr(d, key);
            if (arr == null) return list;
            foreach (object o in arr)
            {
                if (o == null) continue;
                string v = o.ToString();
                if (v.Length > 0) list.Add(v);
            }
            return list;
        }

        private static object[] Arr(Dictionary<string, object> d, string key)
        {
            if (d == null || !d.ContainsKey(key) || d[key] == null) return null;
            return d[key] as object[];
        }

        private static string S(Dictionary<string, object> d, string key)
        {
            if (d == null || !d.ContainsKey(key) || d[key] == null) return "";
            return d[key].ToString();
        }

        private static bool B(Dictionary<string, object> d, string key)
        {
            if (d == null || !d.ContainsKey(key) || d[key] == null) return false;
            try { return Convert.ToBoolean(d[key]); }
            catch { return false; }
        }

        private static long L(Dictionary<string, object> d, string key)
        {
            if (d == null || !d.ContainsKey(key) || d[key] == null) return 0;
            object o = d[key];
            if (o is long) return (long)o;
            if (o is int) return (int)o;
            if (o is double) return (long)(double)o;
            if (o is decimal) return (long)(decimal)o;
            long v; long.TryParse(o.ToString(), out v); return v;
        }
    }

    // ============ 封面缓存（内存 + 磁盘） ============
    //
    // 服务端已经把 WebP 转成 JPEG（.NET Framework 的 Image 解不了 WebP），
    // 所以这里可以放心用 System.Drawing 解码。
    //
    // 两个关键取舍：
    //   1) 磁盘缓存存原始字节（与尺寸无关），内存缓存存按需缩放的缩略图 ——
    //      一张 355x270 的封面解码后约 380KB，整页 90 张就是 34MB，
    //      不缩放的话海报墙滚动几下内存就上去了。
    //   2) 内存淘汰只移除引用、绝不 Dispose：位图可能正被某个控件绘制。
    internal static class CoverCache
    {
        private const int MaxMemory = 200;      // 内存中最多保留多少张缩略图
        private const int MaxBytes = 8 << 20;   // 单张封面大小上限，与服务端保持一致
        private static readonly object Gate = new object();
        private static readonly Dictionary<string, Bitmap> Memo = new Dictionary<string, Bitmap>();
        private static readonly List<string> Order = new List<string>();
        public static string Dir { get { return Path.Combine(AppPaths.Root, "cache", "covers"); } }
        public static Bitmap Get(string url) { return Get(url, 0); }
        // maxWidth > 0 时按宽度等比缩小，供海报墙/详情页复用同一份实现。
        public static Bitmap Get(string url, int maxWidth)
        {
            if (string.IsNullOrEmpty(url)) return null;
            url = url.Trim();
            string key = url + "@" + maxWidth.ToString(CultureInfo.InvariantCulture);
            lock (Gate)
            {
                Bitmap hit;
                if (Memo.TryGetValue(key, out hit)) { Touch(key); return hit; }
            }
            string path = PathFor(url);
            byte[] data = null;
            try { if (File.Exists(path)) data = File.ReadAllBytes(path); }
            catch { data = null; }
            if (data == null)
            {
                data = Download(url);
                if (data != null) TrySave(path, data);
            }
            if (data == null) return null;
            Bitmap bmp = Decode(data, maxWidth);
            if (bmp == null)
            {
                // 磁盘上可能是坏的半截文件，删掉让它下次重新下
                try { File.Delete(path); } catch { }
                return null;
            }
            lock (Gate)
            {
                Memo[key] = bmp;
                Order.Add(key);
                while (Order.Count > MaxMemory)
                {
                    string old = Order[0];
                    Order.RemoveAt(0);
                    Memo.Remove(old);
                }
            }
            return bmp;
        }

        private static void Touch(string key)
        {
            Order.Remove(key);
            Order.Add(key);
        }

        private static byte[] Download(string url)
        {
            CloudConfig.Load();
            // 走服务端代理是为了绕开防盗链 + 把 WebP 转成 JPEG。没配服务器时退回直连：
            // 能拿到多少算多少，总好过海报墙整片空白。
            string target = CloudConfig.IsConfigured
                ? CloudConfig.ServerUrl + "/api/v1/dytt/cover?url=" + Uri.EscapeDataString(url)
                : url;
            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(target);
                req.Method = "GET";
                req.Timeout = 20000;
                req.ReadWriteTimeout = 20000;
                req.UserAgent = "MagDownloader/" + AppVersion.Number;
                using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                using (Stream rs = resp.GetResponseStream())
                using (MemoryStream ms = new MemoryStream())
                {
                    byte[] buf = new byte[16384];
                    int n;
                    while ((n = rs.Read(buf, 0, buf.Length)) > 0)
                    {
                        ms.Write(buf, 0, n);
                        if (ms.Length > MaxBytes) return null;
                    }
                    return ms.ToArray();
                }
            }
            catch (Exception ex)
            {
                Logger.App("封面下载失败：" + url + " -> " + ex.Message);
                return null;
            }
        }

        private static Bitmap Decode(byte[] data, int maxWidth)
        {
            try
            {
                using (MemoryStream ms = new MemoryStream(data))
                using (Image img = Image.FromStream(ms, false, true))
                    return Scale(img, maxWidth);
            }
            catch (Exception ex)
            {
                // 最常见的原因是服务端还是旧版本、直接把 WebP 透传下来了
                Logger.App("封面解码失败（服务端未转码？）：" + ex.Message);
                return null;
            }
        }

        private static Bitmap Scale(Image src, int maxWidth)
        {
            if (maxWidth <= 0 || src.Width <= maxWidth) return new Bitmap(src);
            int w = maxWidth;
            int h = Math.Max(1, (int)Math.Round(src.Height * (double)maxWidth / src.Width));
            Bitmap b = new Bitmap(w, h);
            using (Graphics g = Graphics.FromImage(b))
            {
                g.InterpolationMode = InterpolationMode.HighQualityBicubic;
                g.PixelOffsetMode = PixelOffsetMode.HighQuality;
                g.SmoothingMode = SmoothingMode.HighQuality;
                g.DrawImage(src, new Rectangle(0, 0, w, h));
            }
            return b;
        }

        private static void TrySave(string path, byte[] data)
        {
            try
            {
                Directory.CreateDirectory(Dir);
                string tmp = path + ".tmp";
                File.WriteAllBytes(tmp, data);
                if (File.Exists(path)) File.Delete(path);
                File.Move(tmp, path);
            }
            catch (Exception ex) { Logger.App("封面写入缓存失败：" + ex.Message); }
        }

        private static string PathFor(string url)
        {
            return Path.Combine(Dir, Hash(url) + ".img");
        }

        private static string Hash(string s)
        {
            using (SHA1 sha = SHA1.Create())
            {
                byte[] h = sha.ComputeHash(Encoding.UTF8.GetBytes(s));
                StringBuilder sb = new StringBuilder(h.Length * 2);
                foreach (byte b in h) sb.Append(b.ToString("x2", CultureInfo.InvariantCulture));
                return sb.ToString();
            }
        }
    }
}
