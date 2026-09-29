using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Globalization;
using System.IO;
using System.Net;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Forms;

namespace MagDownloader
{
    // ============ 配色（深色 · torrentclaw 风） ============
    internal static class Pal
    {
        public static readonly Color Bg         = Color.FromArgb(11, 14, 23);
        public static readonly Color Panel      = Color.FromArgb(18, 22, 38);
        public static readonly Color Border     = Color.FromArgb(38, 45, 71);
        public static readonly Color Text       = Color.FromArgb(231, 235, 245);
        public static readonly Color Muted      = Color.FromArgb(139, 147, 173);
        public static readonly Color Accent     = Color.FromArgb(124, 108, 255);
        public static readonly Color AccentHi   = Color.FromArgb(148, 136, 255);
        public static readonly Color AccentLo   = Color.FromArgb(106, 90, 235);
        public static readonly Color Cyan       = Color.FromArgb(78, 205, 196);
        public static readonly Color Success    = Color.FromArgb(47, 213, 123);
        public static readonly Color Warning    = Color.FromArgb(240, 162, 52);
        public static readonly Color Danger     = Color.FromArgb(255, 92, 109);
        public static readonly Color Track      = Color.FromArgb(28, 34, 58);
        public static readonly Color RowHover   = Color.FromArgb(26, 32, 56);
        public static readonly Color RowSel     = Color.FromArgb(31, 39, 74);
    }

    internal static class Native
    {
        [DllImport("user32.dll")] public static extern bool ReleaseCapture();
        [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr hWnd, int msg, IntPtr wParam, IntPtr lParam);
    }

    internal static class Draw
    {
        public static GraphicsPath Round(Rectangle r, int radius)
        {
            GraphicsPath p = new GraphicsPath();
            if (radius <= 0) { p.AddRectangle(r); return p; }
            int d = radius * 2;
            if (d > r.Width) d = r.Width;
            if (d > r.Height) d = r.Height;
            p.AddArc(r.X, r.Y, d, d, 180, 90);
            p.AddArc(r.Right - d, r.Y, d, d, 270, 90);
            p.AddArc(r.Right - d, r.Bottom - d, d, d, 0, 90);
            p.AddArc(r.X, r.Bottom - d, d, d, 90, 90);
            p.CloseFigure();
            return p;
        }
    }

    // ============ 自绘按钮 ============
    internal class RButton : Control
    {
        private bool hover, down;
        private Color cNormal, cHover, cDown, cText;
        public int Radius = 10;

        public RButton(string text, Color normal, Color hover, Color down, Color fore)
        {
            cNormal = normal; cHover = hover; cDown = down; cText = fore;
            this.Text = text;
            this.Cursor = Cursors.Hand;
            this.Font = new Font("Microsoft YaHei UI", 9.5F, FontStyle.Regular);
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw |
                     ControlStyles.SupportsTransparentBackColor, true);
            this.BackColor = Color.Transparent;
        }

        protected override void OnMouseEnter(EventArgs e) { hover = true; Invalidate(); base.OnMouseEnter(e); }
        protected override void OnMouseLeave(EventArgs e) { hover = false; down = false; Invalidate(); base.OnMouseLeave(e); }
        protected override void OnMouseDown(MouseEventArgs e) { down = true; Invalidate(); base.OnMouseDown(e); }
        protected override void OnMouseUp(MouseEventArgs e) { down = false; Invalidate(); base.OnMouseUp(e); }

        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            Rectangle r = new Rectangle(0, 0, Width - 1, Height - 1);
            Color fill = down ? cDown : (hover ? cHover : cNormal);
            if (!Enabled) fill = Color.FromArgb(30, 36, 60);

            using (GraphicsPath path = Draw.Round(r, Radius))
            {
                if (cNormal == Pal.Accent)
                {
                    using (LinearGradientBrush b = new LinearGradientBrush(r,
                        Color.FromArgb(Math.Min(255, fill.R + 10), Math.Min(255, fill.G + 14), Math.Min(255, fill.B + 18)),
                        Color.FromArgb(Math.Max(0, fill.R - 10), Math.Max(0, fill.G - 12), Math.Max(0, fill.B - 14)),
                        LinearGradientMode.Vertical))
                        g.FillPath(b, path);
                }
                else
                {
                    using (SolidBrush b = new SolidBrush(fill)) g.FillPath(b, path);
                }
                using (Pen pen = new Pen(Pal.Border, 1f)) g.DrawPath(pen, path);
            }

            Color tc = Enabled ? cText : Pal.Muted;
            TextRenderer.DrawText(g, Text, Font, r, tc,
                TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
        }
    }

    // ============ 窗口小按钮 ============
    internal class WinBtn : Control
    {
        private bool hover;
        private int kind;
        private Color glyph;

        // Kind 可在运行时改：最大化按钮要在「方框」与「还原」之间切换图标。
        public int Kind
        {
            get { return kind; }
            set { kind = value; }
        }

        public WinBtn(int kind) : this(kind, Color.FromArgb(90, 96, 108)) { }
        public WinBtn(int kind, Color glyphColor)
        {
            this.kind = kind;
            this.glyph = glyphColor;
            this.Cursor = Cursors.Hand;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw |
                     ControlStyles.SupportsTransparentBackColor, true);
            this.BackColor = Color.Transparent;
        }
        protected override void OnMouseEnter(EventArgs e) { hover = true; Invalidate(); base.OnMouseEnter(e); }
        protected override void OnMouseLeave(EventArgs e) { hover = false; Invalidate(); base.OnMouseLeave(e); }
        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            if (hover)
                using (SolidBrush b = new SolidBrush(kind == 1 ? Pal.Danger : Color.FromArgb(60, 255, 255, 255)))
                    g.FillRectangle(b, 0, 0, Width, Height);
            using (Pen pen = new Pen(glyph, 1.4f))
            {
                pen.StartCap = LineCap.Round; pen.EndCap = LineCap.Round;
                int cx = Width / 2, cy = Height / 2;
                if (kind == 0) g.DrawLine(pen, cx - 6, cy, cx + 6, cy);
                else if (kind == 2)
                {
                    // 最大化：一个空心方框
                    g.DrawRectangle(pen, cx - 5, cy - 5, 10, 10);
                }
                else if (kind == 3)
                {
                    // 还原：两个错位方框（后面那个只画露出来的两条边）
                    g.DrawRectangle(pen, cx - 6, cy - 6, 9, 9);
                    g.DrawLine(pen, cx - 6 + 3, cy - 6 + 3, cx + 6, cy - 6 + 3);
                    g.DrawLine(pen, cx + 6, cy - 6 + 3, cx + 6, cy + 6);
                    g.DrawLine(pen, cx + 6, cy + 6, cx - 6 + 3, cy + 6);
                }
                else { g.DrawLine(pen, cx - 5, cy - 5, cx + 5, cy + 5); g.DrawLine(pen, cx + 5, cy - 5, cx - 5, cy + 5); }
            }
        }
    }

    // ============ 任务数据 ============
    internal class TaskInfo
    {
        public string Gid, Status, Name, Dir;
        public long TotalLength, CompletedLength, DownloadSpeed, UploadSpeed;
        public int Connections, Seeders;
        public bool IsBt;
        public int ErrorCode;
        public string ErrorMessage = "";
        public double Pct { get { return TotalLength > 0 ? CompletedLength * 100.0 / TotalLength : 0.0; } }
    }

    // ============ aria2 JSON-RPC 客户端 ============
    internal class Aria2Client
    {
        private string url, token;
        private static readonly string[] KEYS = new string[] {
            "gid","status","totalLength","completedLength","downloadSpeed","uploadSpeed",
            "files","bittorrent","dir","connections","numSeeders","errorCode","errorMessage" };

        public Aria2Client(int port, string token)
        {
            this.url = "http://127.0.0.1:" + port + "/jsonrpc";
            this.token = token;
        }

        private object Call(string method, object[] ps)
        {
            JavaScriptSerializer js = new JavaScriptSerializer();
            Dictionary<string, object> req = new Dictionary<string, object>();
            req["jsonrpc"] = "2.0";
            req["id"] = Guid.NewGuid().ToString("N").Substring(0, 8);
            req["method"] = method;
            req["params"] = ps;
            string body = js.Serialize(req);

            HttpWebRequest w = (HttpWebRequest)WebRequest.Create(url);
            w.Method = "POST";
            w.ContentType = "application/json";
            w.Timeout = 6000;
            byte[] data = Encoding.UTF8.GetBytes(body);
            w.ContentLength = data.Length;
            using (Stream s = w.GetRequestStream()) s.Write(data, 0, data.Length);

            string resp;
            using (HttpWebResponse rr = (HttpWebResponse)w.GetResponse())
            using (StreamReader sr = new StreamReader(rr.GetResponseStream(), Encoding.UTF8))
                resp = sr.ReadToEnd();

            Dictionary<string, object> d = (Dictionary<string, object>)js.DeserializeObject(resp);
            if (d.ContainsKey("result")) return d["result"];
            if (d.ContainsKey("error")) throw new Exception("RPC error: " + resp);
            return null;
        }

        public string AddUri(string uri, string dir)
        {
            object opts = new Dictionary<string, object> { { "dir", dir } };
            object r = Call("aria2.addUri", new object[] { Token(), new object[] { uri }, opts });
            return (string)r;
        }

        public void Pause(string gid)   { Call("aria2.pause", new object[] { Token(), gid }); }
        public void ForcePause(string gid) { Call("aria2.forcePause", new object[] { Token(), gid }); }
        public void Unpause(string gid) { Call("aria2.unpause", new object[] { Token(), gid }); }
        public void Remove(string gid)  { Call("aria2.forceRemove", new object[] { Token(), gid }); }
        public void PauseAll()  { Call("aria2.pauseAll", new object[] { Token() }); }
        public void UnpauseAll(){ Call("aria2.unpauseAll", new object[] { Token() }); }
        public void Purge()     { Call("aria2.purgeDownloadResult", new object[] { Token() }); }
        public void Shutdown()  { try { Call("aria2.forceShutdown", new object[] { Token() }); } catch { } }

        public object[] Tell(string method, object[] extra)
        {
            List<object> ps = new List<object>();
            ps.Add(Token());
            if (extra != null) foreach (object e in extra) ps.Add(e);
            ps.Add(KEYS);
            object r = Call(method, ps.ToArray());
            if (r is object[]) return (object[])r;
            if (r == null) return new object[0];
            return new object[] { r };
        }

        public long[] GlobalStat()
        {
            object r = Call("aria2.getGlobalStat", new object[] { Token() });
            Dictionary<string, object> d = (Dictionary<string, object>)r;
            long dl = L(d.ContainsKey("downloadSpeed") ? d["downloadSpeed"] : null);
            long ul = L(d.ContainsKey("uploadSpeed") ? d["uploadSpeed"] : null);
            long na = L(d.ContainsKey("numActive") ? d["numActive"] : null);
            long nw = L(d.ContainsKey("numWaiting") ? d["numWaiting"] : null);
            long st = L(d.ContainsKey("numStopped") ? d["numStopped"] : null);
            long stt = L(d.ContainsKey("numStoppedTotal") ? d["numStoppedTotal"] : null);
            return new long[] { dl, ul, na, nw, st, stt };
        }

        public TaskInfo Status(string gid)
        {
            object r = Call("aria2.tellStatus", new object[] { Token(), gid, KEYS });
            Dictionary<string, object> d = r as Dictionary<string, object>;
            if (d == null) return null;
            return ParseTask(d);
        }

        public List<FileEntry> GetFiles(string gid)
        {
            List<FileEntry> result = new List<FileEntry>();
            object r = Call("aria2.getFiles", new object[] { Token(), gid });
            object[] arr = r as object[];
            if (arr == null) return result;
            for (int i = 0; i < arr.Length; i++)
            {
                Dictionary<string, object> d = arr[i] as Dictionary<string, object>;
                if (d == null) continue;
                FileEntry f = new FileEntry();
                f.Index = (int)L(d.ContainsKey("index") ? d["index"] : null);
                f.Path = S(d.ContainsKey("path") ? d["path"] : null);
                f.Length = L(d.ContainsKey("length") ? d["length"] : null);
                f.Completed = L(d.ContainsKey("completedLength") ? d["completedLength"] : null);
                f.Selected = string.Equals(S(d.ContainsKey("selected") ? d["selected"] : null), "true", StringComparison.OrdinalIgnoreCase);
                result.Add(f);
            }
            return result;
        }

        public void ChangeOption(string gid, Dictionary<string, object> opts)
        {
            Call("aria2.changeOption", new object[] { Token(), gid, opts });
        }

        private string Token() { return "token:" + token; }

        private static long L(object o)
        {
            if (o == null) return 0;
            if (o is int) return (int)o;
            if (o is long) return (long)o;
            if (o is double) return (long)(double)o;
            long v; long.TryParse(o.ToString(), NumberStyles.Integer, CultureInfo.InvariantCulture, out v);
            return v;
        }

        public static List<TaskInfo> ToTasks(object[] arr)
        {
            List<TaskInfo> list = new List<TaskInfo>();
            if (arr == null) return list;
            for (int i = 0; i < arr.Length; i++)
            {
                Dictionary<string, object> d = arr[i] as Dictionary<string, object>;
                if (d == null) continue;
                list.Add(ParseTask(d));
            }
            return list;
        }

        private static TaskInfo ParseTask(Dictionary<string, object> d)
        {
            TaskInfo t = new TaskInfo();
            t.Gid = S(d.ContainsKey("gid") ? d["gid"] : null);
            t.Status = S(d.ContainsKey("status") ? d["status"] : null);
            t.Dir = S(d.ContainsKey("dir") ? d["dir"] : null);
            t.TotalLength = L(d.ContainsKey("totalLength") ? d["totalLength"] : null);
            t.CompletedLength = L(d.ContainsKey("completedLength") ? d["completedLength"] : null);
            t.DownloadSpeed = L(d.ContainsKey("downloadSpeed") ? d["downloadSpeed"] : null);
            t.UploadSpeed = L(d.ContainsKey("uploadSpeed") ? d["uploadSpeed"] : null);
            t.Connections = (int)L(d.ContainsKey("connections") ? d["connections"] : null);
            t.Seeders = (int)L(d.ContainsKey("numSeeders") ? d["numSeeders"] : null);
            t.ErrorCode = (int)L(d.ContainsKey("errorCode") ? d["errorCode"] : null);
            t.ErrorMessage = S(d.ContainsKey("errorMessage") ? d["errorMessage"] : null);

            string bn = "";
            Dictionary<string, object> bt = d.ContainsKey("bittorrent") ? d["bittorrent"] as Dictionary<string, object> : null;
            if (bt != null)
            {
                t.IsBt = true;
                Dictionary<string, object> info = bt.ContainsKey("info") ? bt["info"] as Dictionary<string, object> : null;
                if (info != null && info.ContainsKey("name")) bn = S(info["name"]);
            }

            if (bn.Length == 0)
            {
                object[] files = d.ContainsKey("files") ? d["files"] as object[] : null;
                if (files != null && files.Length > 0)
                {
                    Dictionary<string, object> f0 = files[0] as Dictionary<string, object>;
                    if (f0 != null && f0.ContainsKey("path"))
                    {
                        string path = S(f0["path"]);
                        if (path.IndexOf("[METADATA]", StringComparison.Ordinal) == 0) bn = "";
                        else bn = Path.GetFileName(path);
                    }
                }
            }

            t.Name = bn.Length > 0 ? bn : "解析磁力链接中… (" + t.Gid.Substring(0, 8) + ")";
            return t;
        }

        private static string S(object o) { return o == null ? "" : o.ToString(); }
    }

    // ============ 侧边导航 ============
    internal class NavEntry
    {
        public string Title;
        public int Count;
        public bool Selected;
    }

    internal class SideNav : Panel
    {
        public List<NavEntry> Items = new List<NavEntry>();
        public event EventHandler SelectionChanged;

        public SideNav()
        {
            this.BackColor = Pal.Bg;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw, true);
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.Clear(Pal.Bg);
            int y = 12;
            for (int i = 0; i < Items.Count; i++)
            {
                NavEntry it = Items[i];
                Rectangle r = new Rectangle(10, y, Width - 20, 40);
                if (it.Selected)
                {
                    using (GraphicsPath p = Draw.Round(r, 8))
                    using (SolidBrush b = new SolidBrush(Color.FromArgb(31, 39, 74))) g.FillPath(b, p);
                    using (SolidBrush b = new SolidBrush(Pal.Accent))
                        g.FillRectangle(b, r.X, r.Y + 10, 4, r.Height - 20);
                }
                Color tc = it.Selected ? Pal.Accent : Pal.Text;
                Rectangle tr = new Rectangle(r.X + 14, r.Y, r.Width - 14, r.Height);
                TextRenderer.DrawText(g, it.Title, new Font("Microsoft YaHei UI", 10F,
                    it.Selected ? FontStyle.Bold : FontStyle.Regular), tr, tc,
                    TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
                TextRenderer.DrawText(g, it.Count.ToString(CultureInfo.InvariantCulture),
                    new Font("Segoe UI", 8.5F), new Rectangle(r.X, r.Y, r.Width - 12, r.Height), Pal.Muted,
                    TextFormatFlags.Right | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
                y += 46;
            }
        }

        protected override void OnMouseDown(MouseEventArgs e)
        {
            base.OnMouseDown(e);
            int idx = -1;
            int y = 12;
            for (int i = 0; i < Items.Count; i++)
            {
                Rectangle r = new Rectangle(10, y, Width - 20, 40);
                if (r.Contains(e.Location)) { idx = i; break; }
                y += 46;
            }
            if (idx >= 0)
            {
                for (int i = 0; i < Items.Count; i++) Items[i].Selected = (i == idx);
                Invalidate();
                if (SelectionChanged != null) SelectionChanged(this, EventArgs.Empty);
            }
        }
    }

    // ============ 任务行 ============
    internal class TaskRow : Control
    {
        public TaskInfo Data;
        public bool Selected;
        public bool Hover;

        public TaskRow(TaskInfo d)
        {
            Data = d;
            this.Height = 64;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw, true);
            this.BackColor = Pal.Panel;
            this.Cursor = Cursors.Hand;
        }

        protected override void OnMouseEnter(EventArgs e) { Hover = true; Invalidate(); base.OnMouseEnter(e); }
        protected override void OnMouseLeave(EventArgs e) { Hover = false; Invalidate(); base.OnMouseLeave(e); }

        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;

            Color bg = Selected ? Pal.RowSel : (Hover ? Pal.RowHover : Pal.Panel);
            using (SolidBrush b = new SolidBrush(bg)) g.FillRectangle(b, 0, 0, Width, Height);
            using (Pen pn = new Pen(Color.FromArgb(28, 34, 58), 1f))
                g.DrawLine(pn, 0, Height - 1, Width, Height - 1);

            Rectangle ic = new Rectangle(16, 18, 28, 28);
            DrawStatusIcon(g, ic, Data.Status);

            int xName = 56;
            int right = Width - 16;
            int pctW = 56, speedW = 96, sizeW = 130;

            Rectangle nameR = new Rectangle(xName, 8, right - xName - speedW - 12, 20);
            TextRenderer.DrawText(g, Data.Name, new Font("Microsoft YaHei UI", 9.5F, FontStyle.Bold),
                nameR, Pal.Text, TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding | TextFormatFlags.EndEllipsis);
            TextRenderer.DrawText(g, StatusText(Data.Status), new Font("Microsoft YaHei UI", 8.5F),
                new Rectangle(right - speedW - 12, 10, speedW, 18), StatusColor(Data.Status),
                TextFormatFlags.Right | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding | TextFormatFlags.EndEllipsis);

            int barL = xName, barR = right - speedW - sizeW - pctW - 12;
            int barY = 36, barH = 7;
            Rectangle bar = new Rectangle(barL, barY, barR - barL, barH);
            using (GraphicsPath tp = Draw.Round(bar, barH / 2))
            using (SolidBrush tb = new SolidBrush(Pal.Track)) g.FillPath(tb, tp);
            if (Data.Pct > 0)
            {
                int fw = (int)(bar.Width * Data.Pct / 100.0);
                if (fw < barH) fw = barH;
                if (fw > bar.Width) fw = bar.Width;
                Rectangle fill = new Rectangle(barL, barY, fw, barH);
                using (GraphicsPath fp = Draw.Round(fill, barH / 2))
                using (LinearGradientBrush lb = new LinearGradientBrush(fill, Pal.AccentHi, Pal.Accent, LinearGradientMode.Horizontal))
                    g.FillPath(lb, fp);
            }
            bool resolving = Data.Status == "active" && Data.TotalLength == 0;
            bool stalled = Data.Status == "active" && Data.TotalLength > 0 && Data.Seeders == 0 && Data.DownloadSpeed == 0 && Data.CompletedLength < Data.TotalLength;
            string pctText = resolving ? "解析中" : (stalled ? "等种子" : (Data.Pct.ToString("0.0", CultureInfo.InvariantCulture) + "%"));
            TextRenderer.DrawText(g, pctText,
                new Font("Segoe UI", 8.5F, FontStyle.Bold),
                new Rectangle(barR + 6, barY - 5, pctW, 18), (resolving || stalled) ? Pal.Warning : (Data.Pct >= 100 ? Pal.Success : Pal.Accent),
                TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);

            TextRenderer.DrawText(g, FmtSpeed(Data.DownloadSpeed), new Font("Segoe UI", 9F, FontStyle.Bold),
                new Rectangle(right - speedW - sizeW, 32, speedW, 20), Pal.Text,
                TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
            TextRenderer.DrawText(g, FmtSize(Data.CompletedLength) + "/" + FmtSize(Data.TotalLength),
                new Font("Segoe UI", 8.5F), new Rectangle(right - sizeW, 33, sizeW, 19), Pal.Muted,
                TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
        }

        private void DrawStatusIcon(Graphics g, Rectangle r, string status)
        {
            g.SmoothingMode = SmoothingMode.AntiAlias;
            if (status == "complete")
            {
                using (SolidBrush b = new SolidBrush(Pal.Success)) g.FillEllipse(b, r);
                using (Pen p = new Pen(Color.White, 2f))
                {
                    p.StartCap = LineCap.Round; p.EndCap = LineCap.Round;
                    g.DrawLine(p, r.X + 7, r.Y + 14, r.X + 12, r.Y + 19);
                    g.DrawLine(p, r.X + 12, r.Y + 19, r.X + 21, r.Y + 9);
                }
            }
            else if (status == "paused")
            {
                using (SolidBrush b = new SolidBrush(Pal.Warning)) g.FillEllipse(b, r);
                using (SolidBrush w = new SolidBrush(Color.White))
                {
                    g.FillRectangle(w, r.X + 9, r.Y + 9, 5, 10);
                    g.FillRectangle(w, r.X + 15, r.Y + 9, 5, 10);
                }
            }
            else if (status == "error")
            {
                using (SolidBrush b = new SolidBrush(Pal.Danger)) g.FillEllipse(b, r);
                using (Pen p = new Pen(Color.White, 2f))
                {
                    p.StartCap = LineCap.Round; p.EndCap = LineCap.Round;
                    g.DrawLine(p, r.X + 9, r.Y + 9, r.X + 19, r.Y + 19);
                    g.DrawLine(p, r.X + 19, r.Y + 9, r.X + 9, r.Y + 19);
                }
            }
            else
            {
                using (SolidBrush b = new SolidBrush(Pal.Accent)) g.FillEllipse(b, r);
                using (Pen p = new Pen(Color.White, 2f))
                {
                    p.StartCap = LineCap.Round; p.EndCap = LineCap.Round;
                    g.DrawLine(p, r.X + 14, r.Y + 6, r.X + 14, r.Y + 18);
                }
                Point[] tri = new Point[] {
                    new Point(r.X + 10, r.Y + 17), new Point(r.X + 18, r.Y + 17), new Point(r.X + 14, r.Y + 22) };
                g.FillPolygon(Brushes.White, tri);
            }
        }

        internal static string StatusText(string s)
        {
            if (s == "active") return "下载中";
            if (s == "waiting") return "排队中";
            if (s == "paused") return "已暂停";
            if (s == "complete") return "已完成";
            if (s == "error") return "出错";
            if (s == "removed") return "已移除";
            return s;
        }
        internal static Color StatusColor(string s)
        {
            if (s == "active") return Pal.Accent;
            if (s == "waiting") return Pal.Muted;
            if (s == "paused") return Pal.Warning;
            if (s == "complete") return Pal.Success;
            if (s == "error") return Pal.Danger;
            return Pal.Muted;
        }

        internal static string FmtSize(long b)
        {
            if (b <= 0) return "0 B";
            double v = b;
            string[] u = new string[] { "B", "KB", "MB", "GB", "TB", "PB" };
            int i = 0;
            while (v >= 1024 && i < u.Length - 1) { v /= 1024.0; i++; }
            return v.ToString(i == 0 ? "0" : "0.0", CultureInfo.InvariantCulture) + " " + u[i];
        }
        internal static string FmtSpeed(long bps)
        {
            if (bps <= 0) return "--";
            return FmtSize(bps) + "/s";
        }
    }

    // ============ 任务列表面板 ============
    internal class TaskListPanel : Panel
    {
        private const int HeaderH = 36;
        private const int RowH = 64;
        private List<TaskRow> rows = new List<TaskRow>();
        private ToolTip tip;
        public HashSet<string> Selected = new HashSet<string>();
        public event EventHandler SelectionChanged;
        public event Action<TaskInfo> DoubleClickTask;

        public TaskListPanel()
        {
            this.BackColor = Pal.Panel;
            this.AutoScroll = true;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw, true);
            tip = new ToolTip();
            tip.AutoPopDelay = 20000;
            tip.InitialDelay = 400;
            tip.ReshowDelay = 100;
            tip.ShowAlways = true;
        }

        public void SetTasks(List<TaskInfo> tasks)
        {
            Dictionary<string, TaskInfo> byGid = new Dictionary<string, TaskInfo>();
            foreach (TaskInfo t in tasks) byGid[t.Gid] = t;

            for (int i = rows.Count - 1; i >= 0; i--)
            {
                if (!byGid.ContainsKey(rows[i].Data.Gid))
                {
                    this.Controls.Remove(rows[i]); rows[i].Dispose(); rows.RemoveAt(i);
                }
            }
            for (int i = 0; i < rows.Count; i++) rows[i].Data = byGid[rows[i].Data.Gid];

            foreach (TaskInfo t in tasks)
            {
                if (!Exists(t.Gid))
                {
                    TaskRow r = new TaskRow(t);
                    r.Selected = Selected.Contains(t.Gid);
                    r.Click += delegate(object s, EventArgs e) { OnRowClicked(r); };
                    r.MouseDoubleClick += delegate(object s, MouseEventArgs e) { OnRowDoubleClick(r); };
                    this.Controls.Add(r);
                    rows.Add(r);
                }
            }
            for (int i = 0; i < rows.Count; i++) SetTip(rows[i]);
            LayoutRows();
            this.Invalidate();
        }

        private void SetTip(TaskRow r)
        {
            if (r == null || r.Data == null) return;
            string msg = r.Data.Name;
            msg += "\n状态：" + TaskRow.StatusText(r.Data.Status);
            if (r.Data.ErrorCode != 0 || r.Data.ErrorMessage.Length > 0)
            {
                string em = r.Data.ErrorMessage;
                int cut = em.IndexOf("errorCode=", StringComparison.Ordinal);
                if (em.IndexOf('\n') >= 0) em = em.Split('\n')[0];
                msg += "\n错误码 " + r.Data.ErrorCode + "：" + em;
            }
            else if (r.Data.TotalLength > 0)
            {
                msg += "\n大小：" + TaskRow.FmtSize(r.Data.CompletedLength) + " / " + TaskRow.FmtSize(r.Data.TotalLength);
            }
            msg += "\n位置：" + r.Data.Dir;
            tip.SetToolTip(r, msg);
        }

        private bool Exists(string gid)
        {
            for (int i = 0; i < rows.Count; i++) if (rows[i].Data.Gid == gid) return true;
            return false;
        }

        private void LayoutRows()
        {
            int w = Math.Max(ClientSize.Width - SystemInformation.VerticalScrollBarWidth - 2, 200);
            for (int i = 0; i < rows.Count; i++)
                rows[i].SetBounds(0, HeaderH + i * RowH, w, RowH);
        }

        protected override void OnResize(EventArgs e) { base.OnResize(e); LayoutRows(); Invalidate(); }

        private void OnRowClicked(TaskRow r)
        {
            bool ctrl = (Control.ModifierKeys & Keys.Control) != 0;
            if (!ctrl) Selected.Clear();
            if (Selected.Contains(r.Data.Gid)) Selected.Remove(r.Data.Gid);
            else Selected.Add(r.Data.Gid);
            foreach (TaskRow row in rows) row.Selected = Selected.Contains(row.Data.Gid);
            if (SelectionChanged != null) SelectionChanged(this, EventArgs.Empty);
        }

        private void OnRowDoubleClick(TaskRow r)
        {
            if (DoubleClickTask != null) DoubleClickTask(r.Data);
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            base.OnPaint(e);
            Graphics g = e.Graphics;
            g.Clear(Pal.Panel);

            Rectangle hr = new Rectangle(0, 0, ClientSize.Width, HeaderH);
            using (SolidBrush b = new SolidBrush(Color.FromArgb(15, 19, 32))) g.FillRectangle(b, hr);
            Font hf = new Font("Microsoft YaHei UI", 8.5F, FontStyle.Regular);
            TextRenderer.DrawText(g, "文件名", hf, new Rectangle(56, 0, 200, HeaderH), Pal.Muted,
                TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
            g.DrawLine(new Pen(Pal.Border), 0, HeaderH - 1, ClientSize.Width, HeaderH - 1);

            if (rows.Count == 0)
            {
                string tip = "暂无任务，点击左上角「新建」添加磁力链接或下载地址";
                TextRenderer.DrawText(g, tip, new Font("Microsoft YaHei UI", 10F),
                    new Rectangle(20, HeaderH + 40, ClientSize.Width - 40, 24), Pal.Muted,
                    TextFormatFlags.HorizontalCenter | TextFormatFlags.NoPadding);
            }
        }
    }

    // ============ 新建任务对话框 ============
    internal class NewTaskDialog : Form
    {
        public TextBox UrlBox, DirBox;
        public RButton BtnDownload, BtnCancel;
        public bool Confirmed;

        public NewTaskDialog(string defaultDir)
        {
            this.Text = "新建下载任务";
            this.FormBorderStyle = FormBorderStyle.None;
            this.StartPosition = FormStartPosition.CenterParent;
            this.ClientSize = new Size(560, 250);
            this.BackColor = Pal.Panel;
            this.Font = new Font("Microsoft YaHei UI", 9F);

            Panel bar = new Panel();
            bar.Dock = DockStyle.Top;
            bar.Height = 40;
            bar.BackColor = Color.FromArgb(15, 19, 32);
            bar.MouseDown += delegate(object s, MouseEventArgs e)
            {
                if (e.Button == MouseButtons.Left)
                {
                    Native.ReleaseCapture();
                    Native.SendMessage(this.Handle, 0x00A1, (IntPtr)2, IntPtr.Zero);
                }
            };
            this.Controls.Add(bar);

            Label t = new Label();
            t.Text = "新建下载任务";
            t.Font = new Font("Microsoft YaHei UI", 10.5F, FontStyle.Bold);
            t.ForeColor = Pal.Text;
            t.Location = new Point(14, 9);
            t.AutoSize = true;
            bar.Controls.Add(t);

            WinBtn btnX = new WinBtn(1);
            btnX.Size = new Size(36, 30);
            btnX.Location = new Point(560 - 40, 5);
            btnX.Click += delegate { this.Close(); };
            bar.Controls.Add(btnX);

            Label l1 = new Label();
            l1.Text = "下载链接（磁力 / HTTP / HTTPS）";
            l1.ForeColor = Pal.Muted;
            l1.Location = new Point(18, 52);
            l1.AutoSize = true;
            this.Controls.Add(l1);

            Panel urlWrap = new Panel();
            urlWrap.Location = new Point(18, 74);
            urlWrap.Size = new Size(524, 46);
            urlWrap.BackColor = Color.FromArgb(26, 32, 56);
            urlWrap.Paint += delegate(object s, PaintEventArgs e)
            {
                Graphics g = e.Graphics;
                g.SmoothingMode = SmoothingMode.AntiAlias;
                using (GraphicsPath p = Draw.Round(new Rectangle(0, 0, urlWrap.Width - 1, urlWrap.Height - 1), 6))
                using (Pen pen = new Pen(Pal.Border, 1f)) g.DrawPath(pen, p);
            };
            this.Controls.Add(urlWrap);

            UrlBox = new TextBox();
            UrlBox.Location = new Point(10, 7);
            UrlBox.Size = new Size(urlWrap.Width - 20, 30);
            UrlBox.Anchor = AnchorStyles.Left | AnchorStyles.Right;
            UrlBox.BorderStyle = BorderStyle.None;
            UrlBox.Font = new Font("Consolas", 9.5F);
            UrlBox.BackColor = Color.FromArgb(26, 32, 56);
            UrlBox.ForeColor = Pal.Text;
            urlWrap.Controls.Add(UrlBox);

            Label l2 = new Label();
            l2.Text = "保存到";
            l2.ForeColor = Pal.Muted;
            l2.Location = new Point(18, 130);
            l2.AutoSize = true;
            this.Controls.Add(l2);

            DirBox = new TextBox();
            DirBox.Location = new Point(18, 152);
            DirBox.Size = new Size(410, 26);
            DirBox.Text = defaultDir;
            DirBox.BackColor = Color.FromArgb(26, 32, 56);
            DirBox.ForeColor = Pal.Text;
            this.Controls.Add(DirBox);

            RButton browse = new RButton("浏览", Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            browse.Location = new Point(436, 150);
            browse.Size = new Size(106, 30);
            browse.Click += delegate
            {
                FolderBrowserDialog fd = new FolderBrowserDialog();
                fd.Description = "选择保存位置";
                if (Directory.Exists(DirBox.Text)) fd.SelectedPath = DirBox.Text;
                if (fd.ShowDialog(this) == DialogResult.OK) DirBox.Text = fd.SelectedPath;
            };
            this.Controls.Add(browse);

            BtnCancel = new RButton("取消", Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            BtnCancel.Location = new Point(330, 200);
            BtnCancel.Size = new Size(100, 34);
            BtnCancel.Click += delegate { this.Close(); };
            this.Controls.Add(BtnCancel);

            BtnDownload = new RButton("立即下载", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            BtnDownload.Location = new Point(442, 200);
            BtnDownload.Size = new Size(100, 34);
            BtnDownload.Click += delegate
            {
                string u = (UrlBox.Text ?? "").Trim();
                if (u.Length == 0) { MessageBox.Show(this, "请输入下载链接。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
                if (!u.StartsWith("magnet:", StringComparison.OrdinalIgnoreCase) &&
                    !u.StartsWith("http://", StringComparison.OrdinalIgnoreCase) &&
                    !u.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
                {
                    MessageBox.Show(this, "请输入以 magnet: 开头的磁力链接，或 http/https 地址。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                    return;
                }
                if ((DirBox.Text ?? "").Trim().Length == 0) { MessageBox.Show(this, "请选择保存位置。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
                Confirmed = true;
                this.DialogResult = DialogResult.OK;
            };
            this.Controls.Add(BtnDownload);
        }
    }

    // ============ 顶部横幅栏（地平线背景） ============
    internal class HeaderBar : Panel
    {
        public Bitmap Banner;
        public string Title;
        public string SubTitle;

        public HeaderBar()
        {
            this.BackColor = Pal.Panel;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw, true);
        }

        protected override void OnPaintBackground(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            if (Banner != null) g.DrawImage(Banner, ClientRectangle);
            else base.OnPaintBackground(e);
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            base.OnPaint(e);
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;

            if (Title != null)
            {
                using (Font f1 = new Font("Microsoft YaHei UI", 12F, FontStyle.Bold))
                {
                    TextRenderer.DrawText(g, Title, f1, new Rectangle(17, 9, ClientSize.Width - 130, 26), Color.FromArgb(90, 8, 14, 34),
                        TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding | TextFormatFlags.EndEllipsis);
                    TextRenderer.DrawText(g, Title, f1, new Rectangle(15, 7, ClientSize.Width - 130, 26), Color.White,
                        TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding | TextFormatFlags.EndEllipsis);
                }
            }
            if (SubTitle != null)
            {
                using (Font f2 = new Font("Microsoft YaHei UI", 8F))
                    TextRenderer.DrawText(g, SubTitle, f2, new Rectangle(17, 36, ClientSize.Width - 130, 18), Color.FromArgb(225, 255, 255, 255),
                        TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding | TextFormatFlags.EndEllipsis);
            }

            using (Pen pn = new Pen(Color.FromArgb(255, 14, 24, 46), 1f))
                g.DrawLine(pn, 0, ClientSize.Height - 1, ClientSize.Width, ClientSize.Height - 1);
        }
    }

    // ============ 主窗口 ============
    public class MainForm : Form
    {
        private RButton btnNew, btnStart, btnPause, btnDelete, btnClear, btnPreview, btnPlay;
        private SideNav nav;
        private TaskListPanel list;
        private Label lblDown, lblUp, lblCount, lblUser;
        private WinBtn btnMin, btnMax, btnClose;
        private HeaderBar head;

        private Process proc;
        private string enginePath, dataDir;
        private int rpcPort;
        private string rpcToken;
        private Aria2Client aria;
        private System.Threading.Timer pollTimer;
        private volatile bool polling;
        private Bitmap _banner;

        // —— 全屏 / 最大化状态 ——
        // 两种是不同的东西：最大化是填满「工作区」（任务栏还在），
        // 全屏是把标题栏也收掉、盖住任务栏，要按 F11 或 Esc 退出。
        private bool fullscreen = false;
        private Rectangle fullscreenPrevBounds;
        private FormWindowState fullscreenPrevState = FormWindowState.Normal;

        private int viewMode = 0;
        private string lastDir = "";
        private ListView histList;
        private HashSet<string> recordedGids = new HashSet<string>();
        private Dictionary<string, string> gidSource = new Dictionary<string, string>();

        // —— 搜索视图（torrentclaw 风深色） ——
        private Panel searchPanel;
        private TextBox searchBox;
        private ComboBox sortBox;
        private string lastSearchQuery = "";
        private ComboBox catBox;
        private ListView resultList;
        private RButton btnCopyMagnet, btnDownload;
        private List<TorrentResult> searchResults = new List<TorrentResult>();
        private Label searchHint;   // 搜索区底部提示：展示实际检索词 / 过滤说明
        private static readonly Color DarkBg = Color.FromArgb(11, 14, 23);
        private static readonly Color DarkPanel = Color.FromArgb(21, 26, 46);
        private static readonly Color DarkLine = Color.FromArgb(38, 45, 71);
        private static readonly Color DarkText = Color.FromArgb(231, 235, 245);
        private static readonly Color DarkMuted = Color.FromArgb(139, 147, 173);
        private static readonly Color DarkAccent = Color.FromArgb(124, 108, 255);
        private static readonly Color DarkGreen = Color.FromArgb(47, 213, 123);
        private static readonly Color DarkRed = Color.FromArgb(255, 92, 109);

        // —— 云端下载视图（服务端离线下载） ——
        private Panel cloudPanel;
        private TextBox cloudBox;
        private ListView cloudList;
        private RButton btnCloudAdd, btnCloudRefresh, btnCloudFetch, btnCloudDelete;
        private Label cloudServerHint;

        // 底部状态栏里的云端状态/设置入口
        private LinkLabel lnkCloudStatus;

        // ---- 影视库视图（电影天堂：封面 / 名称 / 简介） ----
        private MovieBrowseView movieView;

        private const string ExtraTrackers =
            "https://tracker.zhuqiy.com/announce," +
            "udp://tracker.opentrackr.org:1337/announce," +
            "udp://open.tracker.cl:1337/announce," +
            "udp://tracker.openbittorrent.com:6969/announce," +
            "udp://exodus.desync.com:6969/announce," +
            "udp://tracker.torrent.eu.org:451/announce," +
            "udp://open.demonii.com:1337/announce," +
            "udp://tracker.dler.org:6969/announce," +
            "http://tracker.openbittorrent.com:80/announce," +
            "https://tracker.tamersunion.org/announce";

        private const int WM_NCHITTEST = 0x0084;
        private const int HTCLIENT = 1, HTCAPTION = 2;
        private const int HTLEFT = 10, HTRIGHT = 11, HTTOP = 12, HTTOPLEFT = 13;
        private const int HTTOPRIGHT = 14, HTBOTTOM = 15, HTBOTTOMLEFT = 16, HTBOTTOMRIGHT = 17;

        public MainForm()
        {
            BuildUi();
        }

        protected override void WndProc(ref Message m)
        {
            base.WndProc(ref m);
            if (m.Msg == WM_NCHITTEST && m.Result == (IntPtr)HTCLIENT)
            {
                Point p = PointToClient(new Point(m.LParam.ToInt32() & 0xFFFF, m.LParam.ToInt32() >> 16));
                int b = 6;
                bool left = p.X <= b, right = p.X >= ClientSize.Width - b;
                bool top = p.Y <= b, bottom = p.Y >= ClientSize.Height - b;
                if (top && left) m.Result = (IntPtr)HTTOPLEFT;
                else if (top && right) m.Result = (IntPtr)HTTOPRIGHT;
                else if (bottom && left) m.Result = (IntPtr)HTBOTTOMLEFT;
                else if (bottom && right) m.Result = (IntPtr)HTBOTTOMRIGHT;
                else if (left) m.Result = (IntPtr)HTLEFT;
                else if (right) m.Result = (IntPtr)HTRIGHT;
                else if (top) m.Result = (IntPtr)HTTOP;
                else if (bottom) m.Result = (IntPtr)HTBOTTOM;
            }
        }

        private void BuildUi()
        {
            this.Text = "地平线磁力下载";
            this.FormBorderStyle = FormBorderStyle.None;
            this.ClientSize = new Size(980, 640);
            this.MinimumSize = new Size(780, 520);
            this.StartPosition = FormStartPosition.CenterScreen;
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);
            this.DoubleBuffered = true;

            _banner = LoadBanner();

            head = new HeaderBar();
            head.Banner = _banner;
            head.Title = "地平线磁力下载";
            head.SubTitle = "Horizon · 磁力 / HTTP 下载 · " + AppVersion.Display;
            head.Dock = DockStyle.Top;
            head.Height = 64;
            head.MouseDown += new MouseEventHandler(head_MouseDown);

            btnMin = new WinBtn(0, Color.White);
            btnMin.Size = new Size(40, 32);
            btnMin.Click += delegate { this.WindowState = FormWindowState.Minimized; };
            head.Controls.Add(btnMin);

            btnMax = new WinBtn(2, Color.White);
            btnMax.Size = new Size(40, 32);
            btnMax.Click += delegate { ToggleMaximize(); };
            head.Controls.Add(btnMax);

            btnClose = new WinBtn(1, Color.White);
            btnClose.Size = new Size(40, 32);
            btnClose.Click += delegate { this.Close(); };
            head.Controls.Add(btnClose);

            // 窗口按钮固定在标题栏右上角：随 head（Dock=Top 全宽）尺寸实时重定位，
            // 避免停靠前读取 head.Width 导致按钮错位、遮挡标题文字。
            // 最右边是关闭，往左依次是最大化与最小化，与 Windows 惯例一致。
            head.Resize += delegate(object s, EventArgs e)
            {
                LayoutWindowButtons();
            };
            LayoutWindowButtons();

            // 双击标题栏 = 最大化/还原（Windows 的通用习惯，也是全屏之外最常用的）。
            head.DoubleClick += delegate { ToggleMaximize(); };

            Panel toolbar = new Panel();
            toolbar.Dock = DockStyle.Top;
            toolbar.Height = 50;
            toolbar.BackColor = Pal.Panel;

            btnNew = MakeTool("＋ 新建", toolbar, 14);
            btnStart = MakeTool("▶ 开始", toolbar, 108);
            btnPause = MakeTool("⏸ 暂停", toolbar, 202);
            btnDelete = MakeTool("✕ 删除", toolbar, 296);
            btnPreview = MakeTool("预览文件", toolbar, 390);
            btnPlay = MakeTool("播放", toolbar, 484);
            btnClear = MakeTool("清空列表", toolbar, 578);

            btnNew.Click += delegate { OpenNewTaskDialog(); };
            btnStart.Click += delegate { DoUnpause(); };
            btnPause.Click += delegate { DoPause(); };
            btnDelete.Click += delegate { DoDelete(); };
            btnClear.Click += delegate { DoPurge(); };
            btnPreview.Click += delegate { PreviewSelected(); };
            btnPlay.Click += delegate { PlaySelected(); };

            Panel body = new Panel();
            body.Dock = DockStyle.Fill;
            body.BackColor = Pal.Bg;

            // Fill 控件必须先加入（WinForms 按添加逆序停靠，后加的 content 会先被停靠并叠到 nav 上方，
            // 把任务列表左缘 190px 盖住）。这里把 content 先加、nav 后加。
            Panel content = new Panel();
            content.Dock = DockStyle.Fill;
            content.BackColor = Pal.Panel;
            body.Controls.Add(content);

            nav = new SideNav();
            nav.Width = 190;
            nav.Dock = DockStyle.Left;
            nav.Items.Add(new NavEntry { Title = "正在下载", Count = 0, Selected = true });
            nav.Items.Add(new NavEntry { Title = "已完成", Count = 0, Selected = false });
            nav.Items.Add(new NavEntry { Title = "下载历史", Count = 0, Selected = false });
            nav.Items.Add(new NavEntry { Title = "搜索", Count = 0, Selected = false });
            nav.Items.Add(new NavEntry { Title = "影视库", Count = 0, Selected = false });
            nav.Items.Add(new NavEntry { Title = "云端下载", Count = 0, Selected = false });
            nav.SelectionChanged += delegate
            {
                // 按实际选中的项取索引，新增导航项时不需要再改这里
                for (int i = 0; i < nav.Items.Count; i++)
                    if (nav.Items[i].Selected) { SetViewMode(i); return; }
                SetViewMode(0);
            };
            body.Controls.Add(nav);

            list = new TaskListPanel();
            list.Dock = DockStyle.Fill;
            list.SelectionChanged += delegate { UpdateToolState(); };
            list.DoubleClickTask += delegate(TaskInfo tt) { PlayGid(tt.Gid); };
            content.Controls.Add(list);

            histList = new ListView();
            histList.Dock = DockStyle.Fill;
            histList.View = View.Details;
            histList.FullRowSelect = true;
            histList.GridLines = false;
            histList.BorderStyle = BorderStyle.None;
            histList.HeaderStyle = ColumnHeaderStyle.Nonclickable;
            histList.MultiSelect = false;
            histList.BackColor = Color.FromArgb(18, 22, 38);
            histList.ForeColor = Pal.Text;
            histList.Font = new Font("Microsoft YaHei UI", 9F);
            histList.Columns.Add("文件名", 360);
            histList.Columns.Add("大小", 90);
            histList.Columns.Add("完成时间", 150);
            histList.Columns.Add("状态", 90);
            histList.Visible = false;
            histList.MouseDoubleClick += delegate(object s, MouseEventArgs e) { OpenHistorySelected(); };
            content.Controls.Add(histList);

            BuildSearchView(content);

            BuildMovieView(content);

            BuildCloudView(content);

            Panel status = new Panel();
            status.Dock = DockStyle.Bottom;
            status.Height = 30;
            status.BackColor = Pal.Panel;
            status.Paint += delegate(object s, PaintEventArgs e)
            {
                e.Graphics.DrawLine(new Pen(Pal.Border), 0, 0, status.Width, 0);
            };

            lblDown = new Label();
            lblDown.Text = "⬇ 0 B/s";
            lblDown.ForeColor = Pal.Accent;
            lblDown.Font = new Font("Segoe UI", 8.5F, FontStyle.Bold);
            lblDown.Location = new Point(16, 7);
            lblDown.AutoSize = true;
            status.Controls.Add(lblDown);

            lblUp = new Label();
            lblUp.Text = "⬆ 0 B/s";
            lblUp.ForeColor = Pal.Muted;
            lblUp.Font = new Font("Segoe UI", 8.5F);
            lblUp.Location = new Point(150, 7);
            lblUp.AutoSize = true;
            status.Controls.Add(lblUp);

            lblCount = new Label();
            lblCount.Text = "下载中 0 · 排队 0 · 完成 0";
            lblCount.ForeColor = Pal.Muted;
            lblCount.Font = new Font("Microsoft YaHei UI", 8.5F);
            lblCount.Location = new Point(280, 7);
            lblCount.AutoSize = true;
            status.Controls.Add(lblCount);

            // 云端状态 + 一键打开服务器设置。
            // 放在状态栏而不是某个视图里：影视库、搜索、云端下载三处都依赖它，
            // 用户在哪一页都能看见、都能改。
            lnkCloudStatus = new LinkLabel();
            lnkCloudStatus.Font = new Font("Microsoft YaHei UI", 8.5F);
            lnkCloudStatus.AutoSize = true;
            lnkCloudStatus.Location = new Point(470, 7);
            lnkCloudStatus.Click += delegate
            {
                if (CloudSettingsUI.Open(this))
                {
                    UpdateCloudStatusRow();
                    if (viewMode == 5) CloudRefresh();
                    if (viewMode == 4 && movieView != null) movieView.LoadLatest();
                }
            };
            status.Controls.Add(lnkCloudStatus);
            UpdateCloudStatusRow();

            lblUser = new Label();
            lblUser.Text = "当前用户：" + Session.User + (Session.IsAdmin ? "（管理员）" : "");
            lblUser.ForeColor = Pal.Muted;
            lblUser.Font = new Font("Microsoft YaHei UI", 8.5F);
            lblUser.TextAlign = ContentAlignment.MiddleRight;
            lblUser.Size = new Size(150, 20);
            lblUser.Anchor = AnchorStyles.Top | AnchorStyles.Right;
            lblUser.Location = new Point(status.Width - 238, 6);
            status.Controls.Add(lblUser);

            RButton btnAccount = new RButton("账号", Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            btnAccount.Size = new Size(66, 24);
            btnAccount.Font = new Font("Microsoft YaHei UI", 8.5F);
            btnAccount.Radius = 8;
            btnAccount.Anchor = AnchorStyles.Top | AnchorStyles.Right;
            btnAccount.Location = new Point(status.Width - 78, 3);
            btnAccount.Click += delegate { ShowAccountMenu(btnAccount); };
            status.Controls.Add(btnAccount);

            // 停靠控件的添加顺序很重要：WinForms 按添加的逆序停靠，Fill 控件必须先加入（最后被停靠）。
            this.Controls.Add(body);
            this.Controls.Add(status);
            this.Controls.Add(toolbar);
            this.Controls.Add(head);

            this.FormClosing += new FormClosingEventHandler(MainForm_FormClosing);

            // F11 切换全屏、Esc 退出全屏。
            // KeyPreview=true 让窗体先拿到按键，但输入框仍有优先权——
            // 所以只在「焦点不在文本框里」时才拦，否则打字会触发全屏。
            this.KeyPreview = true;
            this.KeyDown += delegate(object s2, KeyEventArgs e)
            {
                if (fullscreen && e.KeyCode == Keys.Escape)
                {
                    e.Handled = true;
                    ToggleFullscreen();
                    return;
                }
                if (e.KeyCode == Keys.F11)
                {
                    e.Handled = true;
                    ToggleFullscreen();
                }
            };

            // 最大化状态变化时把标题栏按钮的图标换成「还原」方框。
            this.Resize += delegate { SyncWindowButtonGlyphs(); UpdateFreezeMode(); };
        }

        // UpdateCloudStatusRow 刷新状态栏里的云端状态。
        // 「未配置」用警示色，因为它不是错误而是还没做的一步 —— 提示要给出动作。
        private void UpdateCloudStatusRow()
        {
            if (lnkCloudStatus == null) return;
            CloudConfig.Load();
            if (CloudConfig.IsConfigured)
            {
                lnkCloudStatus.Text = "云端：已配置";
                lnkCloudStatus.LinkColor = Pal.Muted;
                lnkCloudStatus.LinkBehavior = LinkBehavior.HoverUnderline;
            }
            else
            {
                lnkCloudStatus.Text = "云端：未配置 · 点此设置（影视库 / 云端加速需要）";
                lnkCloudStatus.LinkColor = Pal.Warning;
                lnkCloudStatus.LinkBehavior = LinkBehavior.AlwaysUnderline;
            }
        }

        // IsTextInputFocused 判断焦点是否在可输入控件上。
        // 用于避免「在搜索框里按 Esc 想清空」之类的操作被全屏快捷键吃掉。
        private bool IsTextInputFocused()
        {
            Control c = this.ActiveControl;
            while (c != null && c.Controls.Count > 0 && !(c is TextBoxBase)) c = c.Controls[0];
            return c is TextBoxBase;
        }

        // SyncWindowButtonGlyphs 让最大化按钮显示当前该显示的形状。
        private void SyncWindowButtonGlyphs()
        {
            if (btnMax == null) return;
            int want = fullscreen
                ? 3
                : (this.WindowState == FormWindowState.Maximized ? 3 : 2);
            if (btnMax.Kind != want) { btnMax.Kind = want; btnMax.Invalidate(); }
        }

        // UpdateFreezeMode 在全屏时禁用「拖边框改大小」的命中测试。
        // 全屏下窗口已经铺满屏幕，还去拖边缘只会把布局拉歪。
        private void UpdateFreezeMode()
        {
            if (fullscreen && this.WindowState != FormWindowState.Normal)
                this.WindowState = FormWindowState.Normal;
        }

        private RButton MakeTool(string text, Panel parent, int x)
        {
            RButton b = new RButton(text, Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            b.Location = new Point(x, 9);
            b.Size = new Size(88, 32);
            b.Radius = 10;
            parent.Controls.Add(b);
            return b;
        }

        // LayoutWindowButtons 把三个窗口按钮贴在标题栏右侧。
        // 单独抽出来是因为它既要在 Resize 里调用，也要在构造末尾调用一次
        // ——停靠前 head.Width 还是默认值，那时算出来的坐标是错的。
        private void LayoutWindowButtons()
        {
            if (btnMin == null || btnMax == null || btnClose == null) return;
            int right = head.Width - 8;
            btnClose.Location = new Point(right - btnClose.Width, 14);
            btnMax.Location = new Point(right - btnClose.Width - btnMax.Width, 14);
            btnMin.Location = new Point(right - btnClose.Width - btnMax.Width - btnMin.Width, 14);
        }

        // ToggleMaximize 在「最大化」与「还原」之间切换。
        // 全屏状态下按它没有意义（本来就盖满屏幕），直接忽略避免状态错乱。
        private void ToggleMaximize()
        {
            if (fullscreen) return;
            this.WindowState = this.WindowState == FormWindowState.Maximized
                ? FormWindowState.Normal
                : FormWindowState.Maximized;
        }

        // ToggleFullscreen 在「全屏」与「还原」之间切换。
        //
        // 做法是先把 WindowState 收回 Normal（否则最大化状态下的 Bounds 是
        // 整个屏幕，记下来再还原会变成「一退出全屏就最大化」），
        // 再用「无边框 + 覆盖整个屏幕」实现全屏。
        // 这里不加 TopMost：盖住任务栏靠的是窗口尺寸铺满屏幕，
        // 而 TopMost 会连别的程序也压在下面，属于滥用。
        private void ToggleFullscreen()
        {
            if (!fullscreen)
            {
                fullscreenPrevState = this.WindowState;
                fullscreenPrevBounds = this.Bounds;

                this.WindowState = FormWindowState.Normal;
                this.FormBorderStyle = FormBorderStyle.None;
                this.Bounds = Screen.FromControl(this).Bounds;

                fullscreen = true;
            }
            else
            {
                fullscreen = false;
                this.FormBorderStyle = FormBorderStyle.None; // 本就是无边框自绘标题栏，保持无边框
                if (fullscreenPrevState == FormWindowState.Maximized)
                {
                    this.WindowState = FormWindowState.Maximized;
                }
                else
                {
                    this.Bounds = fullscreenPrevBounds;
                }
            }
            Invalidate(true);
        }

        private void head_MouseDown(object sender, MouseEventArgs e)
        {
            if (e.Button == MouseButtons.Left)
            {
                Native.ReleaseCapture();
                Native.SendMessage(this.Handle, 0x00A1, (IntPtr)HTCAPTION, IntPtr.Zero);
            }
        }

        protected override void OnShown(EventArgs e)
        {
            base.OnShown(e);
            // 引擎提取与启动（含 RPC 就绪等待）放到后台线程，避免首次启动界面卡顿
            ThreadPool.QueueUserWorkItem(delegate
            {
                try { StartDaemon(); }
                catch (Exception ex)
                {
                    RunOnUi(delegate
                    {
                        MessageBox.Show(this, "启动下载引擎失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
                    });
                }
            });
        }

        private Bitmap LoadBanner()
        {
            try
            {
                Assembly asm = Assembly.GetExecutingAssembly();
                using (Stream s = asm.GetManifestResourceStream("horizon.png"))
                {
                    if (s == null) return null;
                    using (MemoryStream ms = new MemoryStream())
                    {
                        s.CopyTo(ms);
                        ms.Position = 0;
                        using (Image tmp = Image.FromStream(ms))
                            return new Bitmap(tmp);
                    }
                }
            }
            catch { return null; }
        }

        private string EnsureEngine()
        {
            string dir = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "MagDownloader");
            Directory.CreateDirectory(dir);
            string target = Path.Combine(dir, "aria2c.exe");

            Assembly asm = Assembly.GetExecutingAssembly();
            using (Stream s = asm.GetManifestResourceStream("aria2c.exe"))
            {
                if (s != null)
                {
                    if (File.Exists(target) && new FileInfo(target).Length == s.Length) return target;
                    using (FileStream fs = new FileStream(target, FileMode.Create, FileAccess.Write))
                    {
                        byte[] buf = new byte[81920];
                        int n;
                        while ((n = s.Read(buf, 0, buf.Length)) > 0) fs.Write(buf, 0, n);
                    }
                    return target;
                }
            }

            // 未内嵌引擎时（编译漏加 /resource:aria2c.exe，或运行不带资源的调试版本）不再直接失败：
            // 先退回 %LOCALAPPDATA%\MagDownloader 下已解包的引擎，再找 PATH 里的 aria2c，
            // 避免整个下载功能因为一个构建参数缺失而完全不可用。
            if (File.Exists(target) && new FileInfo(target).Length > 0)
            {
                Logger.App("程序未内嵌下载引擎，改用已解包的引擎：" + target);
                return target;
            }

            string found = FindEngineOnPath();
            if (found != null)
            {
                Logger.App("程序未内嵌下载引擎，改用 PATH 中的引擎：" + found);
                return found;
            }

            throw new Exception(
                "程序内未找到内嵌引擎。请把 aria2c.exe 放到 " + dir +
                " 后重试，或用带 /resource:aria2c.exe 的命令重新编译客户端。");
        }

        // 在 PATH 里查找 aria2c.exe（未内嵌引擎时的兜底）
        private static string FindEngineOnPath()
        {
            string path = Environment.GetEnvironmentVariable("PATH");
            if (path == null) return null;
            string[] dirs = path.Split(';');
            for (int i = 0; i < dirs.Length; i++)
            {
                string d = dirs[i].Trim();
                if (d.Length == 0) continue;
                try
                {
                    string full = Path.Combine(d, "aria2c.exe");
                    if (File.Exists(full)) return full;
                }
                catch { }
            }
            return null;
        }

        private int FindFreePort()
        {
            System.Net.Sockets.TcpListener l = new System.Net.Sockets.TcpListener(IPAddress.Loopback, 0);
            l.Start();
            int p = ((IPEndPoint)l.LocalEndpoint).Port;
            l.Stop();
            return p;
        }

        private void StartDaemon()
        {
            enginePath = EnsureEngine();
            dataDir = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "MagDownloader");
            lastDir = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "Downloads");

            rpcPort = FindFreePort();
            rpcToken = Guid.NewGuid().ToString("N");

            string args =
                "--enable-rpc --rpc-listen-all=false --rpc-listen-port=" + rpcPort +
                " --rpc-secret=" + rpcToken +
                " --seed-time=0 --continue=true --file-allocation=none" +
                " --enable-dht=true --bt-enable-lpd=true --enable-peer-exchange=true" +
                " --bt-save-metadata=true" +
                " --stream-piece-selector=inorder" +
                " --split=16 --max-connection-per-server=16 --min-split-size=1M" +
                " --max-concurrent-downloads=8" +
                " --dht-listen-port=6881 --listen-port=6881-6999 --bt-max-peers=256" +
                " --bt-tracker=\"" + ExtraTrackers + "\"" +
                " --dir=\"" + lastDir + "\"" +
                " --dht-file-path=\"" + Path.Combine(dataDir, "dht.dat") + "\"" +
                " --summary-interval=0 --console-log-level=warn" +
                " --log=\"" + Path.Combine(dataDir, "aria2.log") + "\"" +
                " --log-level=warn --auto-file-renaming=true";

            ProcessStartInfo psi = new ProcessStartInfo();
            psi.FileName = enginePath;
            psi.Arguments = args;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardError = true;
            psi.RedirectStandardOutput = true;
            proc = Process.Start(psi);

            aria = new Aria2Client(rpcPort, rpcToken);

            bool ok = false;
            for (int i = 0; i < 20; i++)
            {
                try { aria.GlobalStat(); ok = true; break; }
                catch { System.Threading.Thread.Sleep(250); }
            }
            if (!ok) throw new Exception("下载引擎 RPC 未就绪");

            pollTimer = new System.Threading.Timer(delegate { PollTick(); }, null, 500, 1000);
        }

        // 在后台线程执行一段代码；异常（如提供回调）回到 UI 线程处理，避免界面卡死
        private void RunBg(Action work, Action<Exception> onError)
        {
            ThreadPool.QueueUserWorkItem(delegate
            {
                try { work(); }
                catch (Exception ex)
                {
                    if (onError != null) RunOnUi(delegate { onError(ex); });
                }
            });
        }

        // 把一段 UI 更新调度回界面线程
        private void RunOnUi(Action a)
        {
            if (this.IsDisposed || this.Disposing) return;
            if (this.InvokeRequired) { try { this.BeginInvoke(new MethodInvoker(delegate { a(); })); } catch { } }
            else a();
        }

        // 轮询失败日志的节流时间戳。
        // 引擎没起来时每 1.5 秒失败一次，不节流会把日志刷爆到无法阅读，所以同一个
        // 错误 60 秒内只记一条——既保留「界面为什么不动」的线索，又不淹掉其他日志。
        private DateTime lastPollErrorLog = DateTime.MinValue;

        private void LogPollError(string what, Exception ex)
        {
            try
            {
                if ((DateTime.Now - lastPollErrorLog).TotalSeconds < 60) return;
                lastPollErrorLog = DateTime.Now;
                Logger.App(what + "失败：" + ex.Message);
            }
            catch { }
        }

        private void PollTick()
        {
            if (polling || aria == null) return;
            polling = true;
            ThreadPool.QueueUserWorkItem(delegate
            {
                try { PollOnceBg(); }
                catch (Exception ex) { LogPollError("轮询", ex); }
                finally { polling = false; }
            });
        }

        // 主动触发一次后台刷新（供按钮点击等交互后即时更新，不阻塞 UI）
        private void PollNow()
        {
            if (aria == null || polling) return;   // 已有轮询在途则跳过，避免 aria2 响应慢时线程堆叠
            polling = true;
            ThreadPool.QueueUserWorkItem(delegate
            {
                try { PollOnceBg(); }
                catch (Exception ex) { LogPollError("轮询", ex); }
                finally { polling = false; }
            });
        }

        // 后台线程：执行 aria2 JSON-RPC（同步 HTTP，但不阻塞界面）
        private void PollOnceBg()
        {
            object[] active = aria.Tell("aria2.tellActive", null);
            object[] waiting = aria.Tell("aria2.tellWaiting", new object[] { 0, 500 });
            object[] stopped = aria.Tell("aria2.tellStopped", new object[] { 0, 1000 });
            long[] gs = aria.GlobalStat();

            List<TaskInfo> all = new List<TaskInfo>();
            all.AddRange(Aria2Client.ToTasks(active));
            all.AddRange(Aria2Client.ToTasks(waiting));
            all.AddRange(Aria2Client.ToTasks(stopped));

            if (this.IsDisposed || this.Disposing) return;
            this.BeginInvoke(new MethodInvoker(delegate { ApplyPoll(all, gs); }));
        }

        // UI 线程：把后台抓取的数据刷到列表与状态栏
        private void ApplyPoll(List<TaskInfo> all, long[] gs)
        {
            int done = 0;
            List<TaskInfo> view = new List<TaskInfo>();
            foreach (TaskInfo t in all)
            {
                if (t.Status == "complete") done++;
                // 终态（完成/出错）记录进下载历史，重启后仍可查看
                if ((t.Status == "complete" || t.Status == "error") && !recordedGids.Contains(t.Gid))
                {
                    recordedGids.Add(t.Gid);
                    RecordHistory(t);
                }
                if (viewMode == 0) { if (t.Status != "complete") view.Add(t); }
                else if (viewMode == 1) { if (t.Status == "complete") view.Add(t); }
            }
            list.SetTasks(view);

            nav.Items[0].Count = all.Count - done;
            nav.Items[1].Count = done;
            if (nav.Items.Count > 2) nav.Items[2].Count = HistoryStore.All().Count;
            nav.Invalidate();

            lblDown.Text = "⬇ " + TaskRow.FmtSpeed(gs[0]);
            lblUp.Text = "⬆ " + TaskRow.FmtSpeed(gs[1]);
            lblCount.Text = "下载中 " + gs[2] + " · 排队 " + gs[3] + " · 完成 " + done;
            UpdateToolState();

            // 历史视图不随任务轮询刷新（只在切换进来时刷新一次即可），此处仅更新计数
        }

        private void UpdateToolState()
        {
            bool nonList = viewMode == 2 || viewMode == 3 || viewMode == 4;
            bool hasSel = list.Selected.Count > 0;
            btnStart.Enabled = !nonList && hasSel;
            btnPause.Enabled = !nonList && hasSel;
            btnDelete.Enabled = !nonList && hasSel;
            btnPreview.Enabled = !nonList && hasSel;
            btnPlay.Enabled = !nonList && hasSel;
            btnClear.Enabled = !nonList;
        }

        private void OpenNewTaskDialog()
        {
            NewTaskDialog dlg = new NewTaskDialog(lastDir);
            if (dlg.ShowDialog(this) == DialogResult.OK && dlg.Confirmed)
            {
                string u = dlg.UrlBox.Text.Trim();
                string dir = dlg.DirBox.Text.Trim();
                lastDir = dir;
                RunBg(delegate
                {
                    string gid = aria.AddUri(u, dir);
                    gidSource[gid] = u;
                    Logger.Op("新建任务：" + u);
                    RunOnUi(delegate
                    {
                        SwitchToDownloads();
                    });
                    PollNow();
                }, delegate(Exception ex)
                {
                    string m = (ex == null || ex.Message == null) ? "" : ex.Message;
                    if (m.Contains("already registered"))
                        MessageBox.Show(this, "该资源已在下载列表中（通常是之前添加后无速度/无种子的任务）。\n请先在列表里把它删除，再重新添加。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                    else
                        MessageBox.Show(this, "添加任务失败：" + m, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
                });
            }
        }

        private List<string> SelectedGids()
        {
            List<string> gids = new List<string>();
            foreach (Control c in list.Controls)
            {
                TaskRow r = c as TaskRow;
                if (r != null && list.Selected.Contains(r.Data.Gid)) gids.Add(r.Data.Gid);
            }
            return gids;
        }

        private void DoPause()
        {
            List<string> gids = SelectedGids();
            RunBg(delegate
            {
                if (gids.Count == 0) aria.PauseAll();
                else foreach (string g in gids) aria.ForcePause(g);
                Logger.Op("暂停任务 " + gids.Count + " 个");
                PollNow();
            }, delegate(Exception ex)
            {
                MessageBox.Show(this, "暂停失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
            });
        }

        private void DoUnpause()
        {
            List<string> gids = SelectedGids();
            RunBg(delegate
            {
                if (gids.Count == 0) aria.UnpauseAll();
                else foreach (string g in gids) aria.Unpause(g);
                Logger.Op("开始任务 " + gids.Count + " 个");
                PollNow();
            }, delegate(Exception ex)
            {
                MessageBox.Show(this, "开始失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
            });
        }

        private void DoDelete()
        {
            List<string> gids = SelectedGids();
            if (gids.Count == 0) { MessageBox.Show(this, "请先选择要删除的任务。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            RunBg(delegate
            {
                foreach (string g in gids)
                {
                    try { aria.ForcePause(g); } catch { }
                    aria.Remove(g);
                }
                Logger.Op("删除任务 " + gids.Count + " 个");
                RunOnUi(delegate { list.Selected.Clear(); });
                PollNow();
            }, delegate(Exception ex)
            {
                MessageBox.Show(this, "删除失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
            });
        }

        private void DoPurge()
        {
            RunBg(delegate
            {
                aria.Purge();
                Logger.Op("清空列表");
                PollNow();
            }, delegate(Exception ex)
            {
                MessageBox.Show(this, "清空失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
            });
        }

        private static readonly string[] VideoExts = new string[] {
            ".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".ts", ".m4v", ".mpg", ".mpeg", ".3gp", ".rmvb" };

        private static bool IsVideo(string path)
        {
            if (string.IsNullOrEmpty(path)) return false;
            string ext = Path.GetExtension(path);
            if (ext == null) return false;
            ext = ext.ToLowerInvariant();
            foreach (string v in VideoExts) if (ext == v) return true;
            return false;
        }

        private void PreviewSelected()
        {
            List<string> gids = SelectedGids();
            if (gids.Count == 0)
            {
                MessageBox.Show(this, "请先选择要预览的任务。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                return;
            }
            Logger.Op("在线预览：" + gids[0]);
            using (FilePreviewForm f = new FilePreviewForm(aria, gids[0])) f.ShowDialog(this);
            PollNow();
        }

        private void PlaySelected()
        {
            List<string> gids = SelectedGids();
            if (gids.Count == 0)
            {
                MessageBox.Show(this, "请先选择要播放的任务。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                return;
            }
            PlayGid(gids[0]);
        }

        // 播放指定任务中的视频：已下载完整的直接播放；下载中的尝试边下边播（部分文件）
        private void PlayGid(string gid)
        {
            RunBg(delegate
            {
                TaskInfo t = aria.Status(gid);
                if (t == null)
                {
                    RunOnUi(delegate { MessageBox.Show(this, "任务不存在或已被移除。", "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
                    return;
                }
                List<FileEntry> files = aria.GetFiles(gid);
                FileEntry video = null;
                foreach (FileEntry f in files)
                {
                    if (f.Length <= 0) continue;
                    if (!IsVideo(f.Path)) continue;
                    if (video == null || f.Length > video.Length) video = f;
                }
                if (video == null)
                {
                    FileEntry any = null;
                    foreach (FileEntry f in files)
                        if (f.Length > 0 && (any == null || f.Length > any.Length)) any = f;
                    string anyPath = any != null ? Path.Combine(t.Dir, any.Path.Replace('/', Path.DirectorySeparatorChar)) : "";
                    bool anyComplete = any != null && any.Completed >= any.Length;
                    RunOnUi(delegate
                    {
                        if (!string.IsNullOrEmpty(anyPath) && File.Exists(anyPath) && anyComplete)
                        {
                            Logger.Op("打开文件：" + anyPath);
                            Process.Start(anyPath);
                        }
                        else if (t.Dir != null && Directory.Exists(t.Dir))
                        {
                            Process.Start("explorer.exe", "\"" + t.Dir + "\"");
                        }
                        else
                        {
                            MessageBox.Show(this, "文件尚未下载完成，暂无法打开。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                        }
                    });
                    return;
                }

                string full = Path.Combine(t.Dir, video.Path.Replace('/', Path.DirectorySeparatorChar));
                bool complete = video.Completed >= video.Length;

                RunOnUi(delegate
                {
                    if (File.Exists(full) && complete)
                    {
                        Logger.Op("在线播放：" + full);
                        Process.Start(full);
                    }
                    else if (File.Exists(full))
                    {
                        DialogResult r = MessageBox.Show(this,
                            "视频尚未下载完整（已下 " + video.Completed * 100 / video.Length + "%），是否尝试播放未完成文件？\n（可能需要播放器支持流式读取）",
                            "在线播放", MessageBoxButtons.YesNo, MessageBoxIcon.Question);
                        if (r == DialogResult.Yes)
                        {
                            Logger.Op("在线播放（未完成）：" + full);
                            Process.Start(full);
                        }
                    }
                    else
                    {
                        MessageBox.Show(this, "视频文件尚未下载，请先下载。\n文件：" + video.DisplayPath, "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                    }
                });
            }, delegate(Exception ex)
            {
                MessageBox.Show(this, "播放失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
            });
        }

        // ============ 搜索视图（torrentclaw 风深色） ============
        private void BuildSearchView(Panel content)
        {
            searchPanel = new Panel();
            searchPanel.Dock = DockStyle.Fill;
            searchPanel.BackColor = DarkBg;
            searchPanel.Visible = false;

            resultList = new ListView();
            resultList.Dock = DockStyle.Fill;
            resultList.View = View.Details;
            resultList.FullRowSelect = true;
            resultList.GridLines = false;
            resultList.BorderStyle = BorderStyle.None;
            resultList.BackColor = DarkBg;
            resultList.ForeColor = DarkText;
            resultList.HideSelection = false;
            // 「中文名」在前便于阅读；「原始文件名」在后保留英文原名——种子名里的版本、
            // 压制组、语言标记都在原名里，是判断资源真假的依据，不能被译文替代。
            resultList.Columns.Add("中文名", 320);
            resultList.Columns.Add("原始文件名", 400);
            resultList.Columns.Add("类型", 58);
            resultList.Columns.Add("大小", 82);
            resultList.Columns.Add("发布时间", 78);
            resultList.Columns.Add("做种", 56);
            resultList.Columns.Add("下载", 56);
            resultList.MouseDoubleClick += delegate(object s, MouseEventArgs e) { DownloadSelected(); };

            Panel foot = new Panel();
            foot.Dock = DockStyle.Bottom;
            foot.Height = 50;
            foot.BackColor = DarkPanel;
            foot.Paint += delegate(object s, PaintEventArgs e) { e.Graphics.DrawLine(new Pen(DarkLine), 0, 0, foot.Width, 0); };

            searchHint = new Label();
            searchHint.Text = "优先经云端检索（中文片名会先翻译成英文再搜索）· 双击直接下载；选中后可用右侧按钮复制磁力或下载。";
            searchHint.ForeColor = DarkMuted;
            searchHint.Font = new Font("Microsoft YaHei UI", 8.5F);
            searchHint.Location = new Point(16, 16);
            searchHint.AutoSize = true;
            foot.Controls.Add(searchHint);

            btnCopyMagnet = new RButton("复制磁力", DarkPanel, Color.FromArgb(30, 37, 62), Color.FromArgb(24, 30, 52), DarkText);
            btnCopyMagnet.Size = new Size(100, 32);
            btnCopyMagnet.Radius = 10;
            btnCopyMagnet.Click += delegate { CopySelectedMagnet(); };
            foot.Controls.Add(btnCopyMagnet);

            btnDownload = new RButton("下载", DarkAccent, Color.FromArgb(148, 136, 255), Color.FromArgb(106, 90, 235), Color.White);
            btnDownload.Size = new Size(100, 32);
            btnDownload.Radius = 10;
            btnDownload.Click += delegate { DownloadSelected(); };
            foot.Controls.Add(btnDownload);

            foot.Resize += delegate(object s, EventArgs e)
            {
                btnCopyMagnet.Location = new Point(foot.Width - 216, 9);
                btnDownload.Location = new Point(foot.Width - 106, 9);
            };

            Panel bar = new Panel();
            bar.Dock = DockStyle.Top;
            bar.Height = 66;
            bar.BackColor = DarkBg;

            searchBox = new TextBox();
            searchBox.Location = new Point(16, 18);
            searchBox.Size = new Size(470, 30);
            searchBox.Font = new Font("Microsoft YaHei UI", 10F);
            searchBox.BorderStyle = BorderStyle.FixedSingle;
            searchBox.BackColor = DarkPanel;
            searchBox.ForeColor = DarkText;
            searchBox.KeyDown += delegate(object s, KeyEventArgs e) { if (e.KeyCode == Keys.Enter) DoSearch(); };
            bar.Controls.Add(searchBox);

            catBox = new ComboBox();
            catBox.Location = new Point(494, 18);
            catBox.Size = new Size(118, 30);
            catBox.DropDownStyle = ComboBoxStyle.DropDownList;
            catBox.BackColor = DarkPanel;
            catBox.ForeColor = DarkText;
            catBox.Items.AddRange(new object[] { "全部", "电影", "剧集", "4K电影", "音乐", "游戏", "软件" });
            catBox.SelectedIndex = 0;
            bar.Controls.Add(catBox);

            RButton btnSearch = new RButton("搜索", DarkAccent, Color.FromArgb(148, 136, 255), Color.FromArgb(106, 90, 235), Color.White);
            btnSearch.Location = new Point(622, 16);
            btnSearch.Size = new Size(98, 34);
            btnSearch.Radius = 10;
            btnSearch.Click += delegate { DoSearch(); };
            bar.Controls.Add(btnSearch);

            // 排序方式。默认「综合」= 服务端顺序（热度分档 + 同档看新旧），
            // 换排序不重新请求，只重排已有结果，所以切起来是瞬时的。
            Label lblSort = new Label();
            lblSort.Text = "排序";
            lblSort.ForeColor = DarkMuted;
            lblSort.Font = new Font("Microsoft YaHei UI", 9F);
            lblSort.Location = new Point(736, 24);
            lblSort.AutoSize = true;
            bar.Controls.Add(lblSort);

            sortBox = new ComboBox();
            sortBox.Location = new Point(774, 18);
            sortBox.Size = new Size(118, 30);
            sortBox.DropDownStyle = ComboBoxStyle.DropDownList;
            sortBox.BackColor = DarkPanel;
            sortBox.ForeColor = DarkText;
            sortBox.Items.AddRange(new object[] { "综合", "做种最多", "最新发布", "体积最大" });
            sortBox.SelectedIndex = 0;
            sortBox.SelectedIndexChanged += delegate
            {
                TorrentSearch.SortMode = sortBox.SelectedIndex;
                FillResults(searchResults, lastSearchQuery);
            };
            bar.Controls.Add(sortBox);

            searchPanel.Controls.Add(resultList);   // Fill 先加入
            searchPanel.Controls.Add(foot);         // Bottom
            searchPanel.Controls.Add(bar);          // Top

            content.Controls.Add(searchPanel);
        }

        private void DoSearch()
        {
            string q = (searchBox == null ? "" : searchBox.Text ?? "").Trim();
            if (q.Length == 0) { MessageBox.Show(this, "请输入搜索关键词。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            string cat = catBox == null ? "0" : TorrentSearch.CatIdFor(catBox.SelectedIndex);
            RunBg(delegate
            {
                List<TorrentResult> rs = TorrentSearch.Search(q, cat);
                RunOnUi(delegate { FillResults(rs, q); });
            }, delegate(Exception ex)
            {
                RunOnUi(delegate { MessageBox.Show(this, "搜索失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
            });
        }

        private void FillResults(List<TorrentResult> rs, string q)
        {
            searchResults = rs ?? new List<TorrentResult>();
            lastSearchQuery = q ?? "";
            // 排序在填充时应用，而不是在请求参数里：换排序方式不必重新联网，
            // 也就不会因为重排又等一次翻译。
            TorrentSearch.SortResults(searchResults);
            resultList.BeginUpdate();
            resultList.Items.Clear();
            foreach (TorrentResult r in searchResults)
            {
                ListViewItem it = new ListViewItem(r.DisplayName);
                it.SubItems.Add(r.Name);
                it.SubItems.Add(r.CatLabel);
                it.SubItems.Add(TorrentResult.FmtSize(r.Size));
                it.SubItems.Add(r.AddedLabel);
                ListViewItem.ListViewSubItem sSeed = new ListViewItem.ListViewSubItem(it, "▲ " + r.Seeders);
                sSeed.ForeColor = DarkGreen;
                it.SubItems.Add(sSeed);
                ListViewItem.ListViewSubItem sLeech = new ListViewItem.ListViewSubItem(it, "▼ " + r.Leechers);
                sLeech.ForeColor = DarkRed;
                it.SubItems.Add(sLeech);
                it.UseItemStyleForSubItems = false;
                it.Tag = r;
                resultList.Items.Add(it);
            }
            resultList.EndUpdate();
            UpdateSearchHint(q);
            Logger.Op("搜索「" + q + "」：" + searchResults.Count + " 条结果"
                + (TorrentSearch.LastUsedCloud ? "（云端）" : "（本地）")
                + (TorrentSearch.LastSearchTerm.Length > 0 && TorrentSearch.LastSearchTerm != q
                    ? " 实际检索词：" + TorrentSearch.LastSearchTerm : ""));
            if (searchResults.Count == 0)
            {
                string msg = "未找到相关种子，换个关键词试试。";
                if (TorrentSearch.LastHint.Length > 0) msg = TorrentSearch.LastHint;
                MessageBox.Show(this, msg, "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
        }

        // 把「实际检索词 / 过滤情况」写到底部提示，避免用户误以为搜到的是目标影片。
        private void UpdateSearchHint(string q)
        {
            if (searchHint == null) return;
            string text;
            if (TorrentSearch.LastHint.Length > 0)
            {
                text = TorrentSearch.LastHint;
            }
            else if (TorrentSearch.LastSearchTerm.Length > 0 && TorrentSearch.LastSearchTerm != q)
            {
                text = "已把「" + q + "」翻译为「" + TorrentSearch.LastSearchTerm + "」检索 · 共 "
                    + searchResults.Count + " 条（已按片名过滤无关结果）";
            }
            else
            {
                text = (TorrentSearch.LastUsedCloud ? "云端" : "本地") + "检索 · 共 " + searchResults.Count + " 条";
                if (!TorrentSearch.LastUsedCloud)
                    text += " · 云端不可用，中文关键词结果可能不准确";
                else
                {
                    // 显示有多少条是最近一年内发布的，让「有没有新片源」一眼可见。
                    int fresh = 0;
                    long yearAgo = DateTimeOffset.Now.AddDays(-365).ToUnixTimeSeconds();
                    foreach (TorrentResult r in searchResults)
                        if (r.Added >= yearAgo) fresh++;
                    if (fresh > 0) text += " · 其中 " + fresh + " 条为近一年发布";
                }
            }
            searchHint.Text = text;
        }

        private void CopySelectedMagnet()
        {
            if (resultList == null || resultList.SelectedItems.Count == 0)
            { MessageBox.Show(this, "请先在结果里选中一个种子。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            TorrentResult r = resultList.SelectedItems[0].Tag as TorrentResult;
            if (r == null) return;
            try { Clipboard.SetText(r.Magnet); Logger.Op("复制磁力：" + r.Name); }
            catch { }
            MessageBox.Show(this, "已复制磁力链接到剪贴板。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
        }

        private void DownloadSelected()
        {
            if (resultList == null || resultList.SelectedItems.Count == 0)
            { MessageBox.Show(this, "请先在结果里选中一个种子。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            TorrentResult r = resultList.SelectedItems[0].Tag as TorrentResult;
            if (r == null) return;
            if (aria == null) { MessageBox.Show(this, "下载引擎尚未就绪，请稍后再试。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            string magnet = r.Magnet;
            RunBg(delegate
            {
                string gid = aria.AddUri(magnet, lastDir);
                gidSource[gid] = magnet;
                Logger.Op("搜索下载：" + r.Name);
                RunOnUi(delegate { SwitchToDownloads(); });
                PollNow();
            }, delegate(Exception ex)
            {
                RunOnUi(delegate { MessageBox.Show(this, "添加下载失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
            });
        }

        private void SwitchToDownloads()
        {
            viewMode = 0;
            for (int i = 0; i < nav.Items.Count; i++) nav.Items[i].Selected = (i == 0);
            nav.Invalidate();
            SetViewMode(0);
        }

        // ============ 视图切换（正在下载 / 已完成 / 下载历史 / 搜索） ============
        // ============ 云端下载视图（服务器端离线下载） ============
        private void BuildCloudView(Panel content)
        {
            cloudPanel = new Panel();
            cloudPanel.Dock = DockStyle.Fill;
            cloudPanel.BackColor = DarkBg;
            cloudPanel.Visible = false;

            cloudList = new ListView();
            cloudList.Dock = DockStyle.Fill;
            cloudList.View = View.Details;
            cloudList.FullRowSelect = true;
            cloudList.GridLines = false;
            cloudList.BorderStyle = BorderStyle.None;
            cloudList.BackColor = DarkBg;
            cloudList.ForeColor = DarkText;
            cloudList.HideSelection = false;
            cloudList.Columns.Add("名称", 520);
            cloudList.Columns.Add("状态", 80);
            cloudList.Columns.Add("大小", 90);
            cloudList.Columns.Add("速度", 90);

            Panel foot = new Panel();
            foot.Dock = DockStyle.Bottom;
            foot.Height = 50;
            foot.BackColor = DarkPanel;
            foot.Paint += delegate(object s, PaintEventArgs e) { e.Graphics.DrawLine(new Pen(DarkLine), 0, 0, foot.Width, 0); };

            Label hint = new Label();
            hint.Text = "粘贴磁力 →「添加到云端」，服务器 24 小时离线下载；完成后「取回」到本地。";
            hint.ForeColor = DarkMuted;
            hint.Font = new Font("Microsoft YaHei UI", 8.5F);
            hint.Location = new Point(16, 6);
            hint.AutoSize = true;
            foot.Controls.Add(hint);

            btnCloudRefresh = new RButton("刷新", DarkPanel, Color.FromArgb(30, 37, 62), Color.FromArgb(24, 30, 52), DarkText);
            btnCloudRefresh.Size = new Size(88, 32);
            btnCloudRefresh.Radius = 10;
            btnCloudRefresh.Click += delegate { CloudRefresh(); };
            foot.Controls.Add(btnCloudRefresh);

            btnCloudDelete = new RButton("删除", DarkPanel, Color.FromArgb(30, 37, 62), Color.FromArgb(24, 30, 52), DarkText);
            btnCloudDelete.Size = new Size(88, 32);
            btnCloudDelete.Radius = 10;
            btnCloudDelete.Click += delegate { CloudDelete(); };
            foot.Controls.Add(btnCloudDelete);

            btnCloudFetch = new RButton("取回", DarkAccent, Color.FromArgb(148, 136, 255), Color.FromArgb(106, 90, 235), Color.White);
            btnCloudFetch.Size = new Size(88, 32);
            btnCloudFetch.Radius = 10;
            btnCloudFetch.Click += delegate { CloudFetch(); };
            foot.Controls.Add(btnCloudFetch);

            foot.Resize += delegate(object s, EventArgs e)
            {
                btnCloudFetch.Location = new Point(foot.Width - 100, 9);
                btnCloudDelete.Location = new Point(foot.Width - 196, 9);
                btnCloudRefresh.Location = new Point(foot.Width - 292, 9);
            };

            Panel bar = new Panel();
            bar.Dock = DockStyle.Top;
            bar.Height = 66;
            bar.BackColor = DarkBg;

            cloudBox = new TextBox();
            cloudBox.Location = new Point(16, 18);
            cloudBox.Size = new Size(520, 30);
            cloudBox.Font = new Font("Microsoft YaHei UI", 10F);
            cloudBox.BorderStyle = BorderStyle.FixedSingle;
            cloudBox.BackColor = DarkPanel;
            cloudBox.ForeColor = DarkText;
            cloudBox.KeyDown += delegate(object s, KeyEventArgs e) { if (e.KeyCode == Keys.Enter) CloudAdd(); };
            bar.Controls.Add(cloudBox);

            btnCloudAdd = new RButton("添加到云端", DarkAccent, Color.FromArgb(148, 136, 255), Color.FromArgb(106, 90, 235), Color.White);
            btnCloudAdd.Location = new Point(546, 16);
            btnCloudAdd.Size = new Size(160, 34);
            btnCloudAdd.Radius = 10;
            btnCloudAdd.Click += delegate { CloudAdd(); };
            bar.Controls.Add(btnCloudAdd);

            cloudPanel.Controls.Add(cloudList);
            cloudPanel.Controls.Add(foot);
            cloudPanel.Controls.Add(bar);

            cloudServerHint = new Label();
            cloudServerHint.ForeColor = DarkMuted;
            cloudServerHint.Font = new Font("Microsoft YaHei UI", 8.5F);
            cloudServerHint.Location = new Point(16, 26);
            cloudServerHint.AutoSize = true;
            cloudServerHint.Text = "";
            foot.Controls.Add(cloudServerHint);

            content.Controls.Add(cloudPanel);
            ProbeServerVersion();
        }

        // 异步探测服务端版本并显示在云端视图底部。
        // 走 /healthz（无需登录），失败就保持空白——探测不到版本不该影响任何功能。
        private void ProbeServerVersion()
        {
            RunBg(delegate
            {
                string v = CloudVersion.Fetch();
                RunOnUi(delegate
                {
                    if (cloudServerHint == null) return;
                    cloudServerHint.Text = v.Length > 0
                        ? "客户端 " + AppVersion.Display + " · 服务端 " + v
                        : "客户端 " + AppVersion.Display + " · 服务端版本未知";
                });
            }, null);
        }

        // 云端调用失败的统一文案：401 时明确告诉用户「登录已过期」，
        // 而不是把「远程服务器返回错误：(401)」这种原始报错直接丢给用户。
        private static string CloudHint(string what, Exception ex)
        {
            string msg = ex == null ? "" : ex.Message;
            if (msg.Contains("401"))
                return what + "：登录状态已过期且自动续期失败，请退出登录后重新登录。";
            return what + "：" + msg;
        }

        private void CloudAdd()
        {
            string m = (cloudBox == null ? "" : cloudBox.Text ?? "").Trim();
            if (m.Length == 0) { MessageBox.Show(this, "请粘贴磁力链接。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            if (!m.StartsWith("magnet:", StringComparison.OrdinalIgnoreCase)) { MessageBox.Show(this, "请输入以 magnet: 开头的磁力链接。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            RunBg(delegate
            {
                CloudApi.AddTorrent(m);
                Logger.Op("云端下载：" + m);
                RunOnUi(delegate { cloudBox.Text = ""; CloudRefresh(); });
            }, delegate(Exception ex)
            {
                RunOnUi(delegate { MessageBox.Show(this, CloudHint("添加到云端失败", ex), "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
            });
        }

        private void CloudRefresh()
        {
            RunBg(delegate
            {
                List<CloudTask> tasks = CloudApi.ListTorrents();
                RunOnUi(delegate { FillCloudList(tasks); });
            }, delegate(Exception ex)
            {
                RunOnUi(delegate { MessageBox.Show(this, CloudHint("获取云端任务失败", ex), "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
            });
        }

        private void FillCloudList(List<CloudTask> tasks)
        {
            cloudList.BeginUpdate();
            cloudList.Items.Clear();
            foreach (CloudTask t in tasks)
            {
                string status = t.Status == "complete" ? "已完成" :
                    (t.Status == "active" ? "下载中" :
                    (t.Status == "resolving" ? "解析中" :
                    (t.Status == "paused" ? "已暂停" :
                    (t.Status == "error" ? "出错" : t.Status))));
                string name = t.Name.Length > 0 ? t.Name : "解析中… (" + t.InfoHash.Substring(0, Math.Min(8, t.InfoHash.Length)) + ")";
                ListViewItem it = new ListViewItem(name);
                it.SubItems.Add(status);
                it.SubItems.Add(t.Total > 0 ? TorrentResult.FmtSize(t.Total) : "-");
                it.SubItems.Add(t.Speed > 0 ? TorrentResult.FmtSize(t.Speed) + "/s" : "-");
                it.Tag = t;
                it.UseItemStyleForSubItems = false;
                if (t.Status == "complete") it.ForeColor = DarkGreen;
                cloudList.Items.Add(it);
            }
            cloudList.EndUpdate();
        }

        private void CloudFetch()
        {
            if (cloudList.SelectedItems.Count == 0) { MessageBox.Show(this, "请先选择一个任务。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            CloudTask t = cloudList.SelectedItems[0].Tag as CloudTask;
            if (t == null) return;
            if (t.Status != "complete") { MessageBox.Show(this, "该任务尚未下载完成，无法取回。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            string defaultName = t.Name.Length > 0 ? t.Name : t.InfoHash;
            using (SaveFileDialog sd = new SaveFileDialog())
            {
                sd.Title = "取回云端文件";
                sd.FileName = defaultName;
                sd.Filter = "所有文件|*.*";
                if (sd.ShowDialog(this) != DialogResult.OK) return;
                string path = sd.FileName;
                RunBg(delegate
                {
                    CloudApi.DownloadFile(t.InfoHash, path);
                    Logger.Op("云端取回：" + t.Name + " -> " + path);
                    RunOnUi(delegate { MessageBox.Show(this, "已取回到：" + path, "完成", MessageBoxButtons.OK, MessageBoxIcon.Information); });
                }, delegate(Exception ex)
                {
                    RunOnUi(delegate { MessageBox.Show(this, CloudHint("取回失败", ex), "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
                });
            }
        }

        private void CloudDelete()
        {
            if (cloudList.SelectedItems.Count == 0) { MessageBox.Show(this, "请先选择一个任务。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            CloudTask t = cloudList.SelectedItems[0].Tag as CloudTask;
            if (t == null) return;
            RunBg(delegate
            {
                CloudApi.DeleteTorrent(t.InfoHash);
                RunOnUi(delegate { CloudRefresh(); });
            }, delegate(Exception ex)
            {
                RunOnUi(delegate { MessageBox.Show(this, CloudHint("删除失败", ex), "错误", MessageBoxButtons.OK, MessageBoxIcon.Error); });
            });
        }

        // 视图索引：0 正在下载 / 1 已完成 / 2 下载历史 / 3 搜索 / 4 影视库 / 5 云端下载
        private void SetViewMode(int m)
        {
            viewMode = m;
            list.Visible = (m == 0 || m == 1);
            histList.Visible = (m == 2);
            searchPanel.Visible = (m == 3);
            if (movieView != null) movieView.Visible = (m == 4);
            if (cloudPanel != null) cloudPanel.Visible = (m == 5);
            if (m == 2) RefreshHistory();
            if (m == 0 || m == 1) PollNow();
            if (m == 3 && searchBox != null) { try { searchBox.Focus(); } catch { } }
            if (m == 4 && movieView != null)
            {
                // 不能进这个视图：先给状态提示，再让用户自己回上一个视图。
                // 不能在这里递归调 SetViewMode 回退——nav 的选中项还停在「影视库」，
                // 下次点它会因为 Selected 没变而不触发事件，界面就卡在这一页了。
                if (!CloudConfig.IsConfigured)
                {
                    movieView.SetUnconfigured();
                }
                else
                {
                    // 首次进入自动拉一次最新更新，省掉一次点击
                    if (movieView.Count == 0) movieView.LoadLatest();
                    movieView.FocusSearch();
                    // 左侧导航项也显示条目数，和其它视图保持一致
                    if (nav.Items.Count > 4) { nav.Items[4].Count = movieView.Count; nav.Invalidate(); }
                }
            }
            if (m == 5)
            {
                if (!CloudConfig.IsConfigured)
                {
                    // 云端下载视图本来就有一行服务端提示，直接写在那儿最省事，
                    // 不用再弹一个每次切过来都出现的对话框。
                    cloudList.Items.Clear();
                    cloudServerHint.Text = "未配置云端服务器 · 点状态栏「云端」设置后即可使用";
                }
                else CloudRefresh();
            }
            UpdateToolState();
        }

        // ============ 影视库视图（电影天堂：封面 / 名称 / 简介） ============
        // 元数据由云端服务 /api/v1/dytt/* 提供（服务端做限速 + 缓存，客户端不直连内容源），
        // 客户端只负责展示；封面统一走云端代理并落本地缓存。
        private void BuildMovieView(Panel content)
        {
            movieView = new MovieBrowseView();
            movieView.Dock = DockStyle.Fill;
            movieView.MagnetSearchRequested += delegate(object s, MovieDetail d)
            {
                SwitchToTorrentSearch(d);
            };
            movieView.ItemsChanged += delegate
            {
                if (nav == null || nav.Items.Count < 5) return;
                nav.Items[4].Count = movieView.Count;
                nav.Invalidate();
            };
            content.Controls.Add(movieView);
        }

        // 从影视库跳到种子搜索：把片名关键词填进搜索框并立即检索
        // 影视库 -> 种子搜索。
        //
        // 这里刻意用「纯片名」而不是站点的 MagnetQueries（形如「云雀叫天录 2026 1080p 磁力」）：
        // 机器翻译对长串后缀处理很差，会把「1080p 磁力」也逐字翻成英文，导致检索词失真；
        // 实测「流浪地球」能翻译成 "The Wandering Earth" 并搜到 41 条，而带后缀的长串搜不到东西。
        private void SwitchToTorrentSearch(MovieDetail d)
        {
            if (d == null) return;
            string keyword = (d.Title ?? "").Trim();
            if (keyword.Length == 0) keyword = (d.Keyword ?? "").Trim();
            if (keyword.Length == 0) return;

            for (int i = 0; i < nav.Items.Count; i++) nav.Items[i].Selected = (i == 3);
            nav.Invalidate();
            SetViewMode(3);
            if (searchBox != null) searchBox.Text = keyword;
            if (catBox != null) catBox.SelectedIndex = 1;   // 默认按「电影」检索
            Logger.Op("影视库跳转搜索：" + keyword + "（原始片名 " + d.Title + "）");
            DoSearch();

            // 中文片名依赖云端翻译检索；若云端不可用或该片没有英文资源，必须说清楚，
            // 否则用户容易把不相关的结果误当成目标影片。
            if (searchResults.Count == 0)
            {
                string msg;
                if (!TorrentSearch.LastUsedCloud)
                {
                    msg = "云端服务不可用，无法把中文片名翻译成英文检索。\n\n"
                        + "「" + d.Title + "」这类中文片名在英文索引站点上直接检索没有意义，"
                        + "请先登录云端服务后重试。";
                }
                else if (TorrentSearch.LastSearchTerm.Length > 0 && TorrentSearch.LastSearchTerm != keyword)
                {
                    msg = "已把「" + d.Title + "」翻译为「" + TorrentSearch.LastSearchTerm + "」检索，但没有找到资源。\n\n"
                        + "该片可能还没有英文资源。可尝试：\n"
                        + "  · 用「云端下载」把磁力交给服务器下载\n"
                        + "  · 复制检索关键词到中文磁力站点搜索";
                }
                else
                {
                    msg = "没有找到《" + d.Title + "》的相关资源，换个关键词或改用云端下载试试。";
                }
                MessageBox.Show(this, msg, "未找到资源", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }
        }

        private void RefreshHistory()
        {
            histList.BeginUpdate();
            histList.Items.Clear();
            List<HistoryEntry> hs = HistoryStore.All();
            foreach (HistoryEntry h in hs)
            {
                ListViewItem it = new ListViewItem(h.Name.Length > 0 ? h.Name : "(未命名)");
                it.SubItems.Add(TaskRow.FmtSize(h.Size));
                it.SubItems.Add(h.Time);
                it.SubItems.Add(h.Status == "complete" ? "已完成" : (h.Status == "error" ? "出错" : h.Status));
                it.Tag = h;
                histList.Items.Add(it);
            }
            histList.EndUpdate();
        }

        private void OpenHistorySelected()
        {
            if (histList.SelectedItems.Count == 0) return;
            HistoryEntry h = histList.SelectedItems[0].Tag as HistoryEntry;
            if (h == null) return;
            string p = h.FilePath;
            try
            {
                if (!string.IsNullOrEmpty(p) && File.Exists(p))
                {
                    Logger.Op("打开历史文件：" + p);
                    Process.Start(p);
                }
                else if (!string.IsNullOrEmpty(p) && Directory.Exists(p))
                {
                    Process.Start("explorer.exe", "\"" + p + "\"");
                }
                else if (!string.IsNullOrEmpty(h.Dir) && Directory.Exists(h.Dir))
                {
                    Process.Start("explorer.exe", "\"" + h.Dir + "\"");
                }
            }
            catch { }
        }

        // 把结束的任务（完成/出错）写入下载历史（非阻塞，仅记录基础信息）
        private void RecordHistory(TaskInfo t)
        {
            try
            {
                HistoryEntry e = new HistoryEntry();
                e.Name = t.Name.Length > 0 ? t.Name : "(未命名)";
                e.Source = gidSource.ContainsKey(t.Gid) ? gidSource[t.Gid] : "";
                e.Dir = t.Dir;
                e.FilePath = Path.Combine(t.Dir, t.Name);
                e.Size = t.TotalLength;
                e.Status = t.Status;
                e.Time = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                HistoryStore.Add(e);
                Logger.Op("记录下载历史：" + e.Name);
            }
            catch { }
        }

        private void MainForm_FormClosing(object sender, FormClosingEventArgs e)
        {
            // 停止轮询，防止关闭过程中后台线程再触发刷新
            if (pollTimer != null) { try { pollTimer.Dispose(); } catch { } pollTimer = null; }

            // 直接终止 aria2c 子进程。不要调用 aria.Shutdown()：那是同步 HTTP 调用，
            // 超时最长 6 秒，正是「关闭窗口很卡」的元凶（且启动命令未配置 --save-session，
            // 优雅关停没有额外收益，直接 Kill 等价且能立刻关窗）。
            try
            {
                if (proc != null)
                {
                    if (!proc.HasExited) proc.Kill();
                    proc.Dispose();
                }
            }
            catch { }
        }

        private void ShowAccountMenu(Control anchor)
        {
            ContextMenuStrip m = new ContextMenuStrip();
            ToolStripMenuItem itPwd = new ToolStripMenuItem("修改密码");
            itPwd.Click += delegate { AccountUI.ChangePassword(this); };
            m.Items.Add(itPwd);

            if (Session.IsAdmin)
            {
                ToolStripMenuItem itUser = new ToolStripMenuItem("用户管理");
                itUser.Click += delegate { AccountUI.UserManage(this); };
                m.Items.Add(itUser);
            }

            m.Items.Add(new ToolStripSeparator());
            ToolStripMenuItem itOut = new ToolStripMenuItem("退出登录");
            itOut.Click += delegate { Logger.Op("注销：" + Session.User); Session.LogoutRequested = true; this.Close(); };
            m.Items.Add(itOut);

            m.Show(anchor, new Point(0, anchor.Height));
        }
    }
}