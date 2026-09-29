using System;
using System.Collections.Generic;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Globalization;
using System.Text;
using System.Threading;
using System.Windows.Forms;

namespace MagDownloader
{
    // ============ 海报墙 ============
    //
    // 用自绘而不是 ListView.LargeIcon：封面是异步从云端取的，
    // 自绘可以「先出文字卡片、图片到位后局部刷新」，不会整块闪烁。
    internal class MoviePosterWall : Control
    {
        private const int PadX = 14, PadY = 12, GapX = 12, GapY = 14;
        private const int CardW = 150, CardH = 150 + 40;
        private const int PosterMaxW = 140;   // 封面按此宽度缩略，控制内存
        private const int MaxQueue = 80;      // 单次最多预取多少张，避免一次打爆网络

        private readonly List<MovieSummary> items = new List<MovieSummary>();
        private readonly Dictionary<string, Bitmap> posters = new Dictionary<string, Bitmap>();
        private readonly object posterGate = new object();

        private int scroll, hoverIndex = -1, selectedIndex = -1, loadToken;

        public event EventHandler<MovieSummary> Activate;
        public event EventHandler NeedMore;
        public event EventHandler SelectionChanged;

        public string EmptyText = "暂无内容";

        public MoviePosterWall()
        {
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);
            TabStop = false;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw |
                     ControlStyles.Selectable, true);
        }

        public int Count { get { return items.Count; } }

        public MovieSummary Selected
        {
            get
            {
                if (selectedIndex < 0 || selectedIndex >= items.Count) return null;
                return items[selectedIndex];
            }
        }

        // ============ 数据 ============

        public void SetItems(List<MovieSummary> list)
        {
            items.Clear();
            if (list != null) items.AddRange(list);
            lock (posterGate) posters.Clear();
            scroll = 0; hoverIndex = -1; selectedIndex = -1;
            Invalidate();
            LoadPosters();
        }

        public void AppendItems(List<MovieSummary> list)
        {
            if (list == null || list.Count == 0) return;
            items.AddRange(list);
            Invalidate();
            LoadPosters();
        }

        // ============ 布局 ============

        private int Columns
        {
            get
            {
                int avail = Math.Max(CardW, ClientSize.Width - PadX * 2);
                return Math.Max(1, (avail + GapX) / (CardW + GapX));
            }
        }

        private int GridLeft
        {
            get
            {
                int total = Columns * CardW + (Columns - 1) * GapX;
                return Math.Max(PadX, (ClientSize.Width - total) / 2);
            }
        }

        private int Rows { get { return (items.Count + Columns - 1) / Columns; } }

        private int MaxScroll
        {
            get
            {
                int content = PadY * 2 + Rows * CardH + Math.Max(0, Rows - 1) * GapY;
                return Math.Max(0, content - ClientSize.Height);
            }
        }

        private Rectangle RectFor(int i)
        {
            int c = Columns;
            int x = GridLeft + (i % c) * (CardW + GapX);
            int y = PadY + (i / c) * (CardH + GapY) - scroll;
            return new Rectangle(x, y, CardW, CardH);
        }

        private int IndexAt(Point p)
        {
            for (int i = 0; i < items.Count; i++)
                if (RectFor(i).Contains(p)) return i;
            return -1;
        }
        // ============ 交互 ============

        protected override void OnMouseMove(MouseEventArgs e)
        {
            base.OnMouseMove(e);
            int i = IndexAt(e.Location);
            if (i != hoverIndex) { hoverIndex = i; Invalidate(); }
        }

        protected override void OnMouseLeave(EventArgs e)
        {
            base.OnMouseLeave(e);
            if (hoverIndex != -1) { hoverIndex = -1; Invalidate(); }
        }

        protected override void OnMouseDown(MouseEventArgs e)
        {
            base.OnMouseDown(e);
            int i = IndexAt(e.Location);
            selectedIndex = i;
            Invalidate();
            if (i >= 0 && SelectionChanged != null) SelectionChanged(this, EventArgs.Empty);
        }

        protected override void OnMouseDoubleClick(MouseEventArgs e)
        {
            base.OnMouseDoubleClick(e);
            ActivateSelected();
        }

        public void ActivateSelected()
        {
            MovieSummary m = Selected;
            if (m != null && Activate != null) Activate(this, m);
        }

        // 滚轮只有控件获得焦点才会送达，悬停时顺手接管焦点
        protected override void OnMouseEnter(EventArgs e)
        {
            base.OnMouseEnter(e);
            try { Focus(); } catch { }
        }

        protected override void OnMouseWheel(MouseEventArgs e)
        {
            base.OnMouseWheel(e);
            int max = MaxScroll;
            if (max <= 0) return;
            int next = scroll - e.Delta;
            if (next < 0) next = 0;
            if (next > max) next = max;
            if (next == scroll) return;
            scroll = next;
            Invalidate();
            if (NeedMore != null && scroll > max - 500) NeedMore(this, EventArgs.Empty);
        }

        protected override bool IsInputKey(Keys keyData)
        {
            if (keyData == Keys.Up || keyData == Keys.Down ||
                keyData == Keys.Left || keyData == Keys.Right ||
                keyData == Keys.Home || keyData == Keys.End) return true;
            return base.IsInputKey(keyData);
        }

        protected override void OnKeyDown(KeyEventArgs e)
        {
            base.OnKeyDown(e);
            if (items.Count == 0) return;
            int c = Columns;
            int cur = selectedIndex < 0 ? 0 : selectedIndex;
            switch (e.KeyCode)
            {
                case Keys.Left: cur -= 1; break;
                case Keys.Right: cur += 1; break;
                case Keys.Up: cur -= c; break;
                case Keys.Down: cur += c; break;
                case Keys.Home: cur = 0; break;
                case Keys.End: cur = items.Count - 1; break;
                case Keys.Enter: ActivateSelected(); return;
                default: return;
            }
            if (cur < 0) cur = 0;
            if (cur >= items.Count) cur = items.Count - 1;
            selectedIndex = cur;
            EnsureVisible(cur);
            Invalidate();
            if (SelectionChanged != null) SelectionChanged(this, EventArgs.Empty);
            e.Handled = true;
        }

        private void EnsureVisible(int index)
        {
            Rectangle r = RectFor(index);
            int contentTop = r.Top + scroll;
            int contentBottom = contentTop + CardH;
            if (contentTop - PadY < scroll) scroll = Math.Max(0, contentTop - PadY);
            else if (contentBottom + PadY > scroll + ClientSize.Height)
                scroll = contentBottom + PadY - ClientSize.Height;
        }

        // ============ 封面异步加载 ============

        private void LoadPosters()
        {
            // 只在 UI 线程取快照，后台线程只读这份列表
            List<string> urls = new List<string>();
            int h = ClientSize.Height;
            for (int i = 0; i < items.Count; i++)
            {
                Rectangle r = RectFor(i);
                if (r.Bottom < -100) continue;
                if (r.Top > h + 100) break;
                string u = items[i].Poster;
                if (u.Length == 0) continue;
                bool have;
                lock (posterGate) have = posters.ContainsKey(u);
                if (have) continue;
                urls.Add(u);
                if (urls.Count >= MaxQueue) break;
            }
            if (urls.Count == 0) return;

            int token = ++loadToken;
            ThreadPool.QueueUserWorkItem(delegate
            {
                foreach (string u in urls)
                {
                    if (token != loadToken || IsDisposed) return;
                    Bitmap b = CoverCache.Get(u, PosterMaxW);
                    if (b == null) continue;
                    lock (posterGate) posters[u] = b;
                    if (token != loadToken) return;
                    InvalidateSafe();
                }
            });
        }

        public void ReloadAfterResize()
        {
            if (scroll > MaxScroll) scroll = MaxScroll;
            LoadPosters();
        }

        private void InvalidateSafe()
        {
            if (IsDisposed || Disposing) return;
            try
            {
                if (InvokeRequired) BeginInvoke(new MethodInvoker(Invalidate));
                else Invalidate();
            }
            catch { }
        }

        // ============ 绘制 ============

        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.Clear(Pal.Bg);

            if (items.Count == 0)
            {
                using (Font f = new Font("Microsoft YaHei UI", 10F))
                    TextRenderer.DrawText(g, EmptyText, f, ClientRectangle, Pal.Muted,
                        TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
                return;
            }

            int h = ClientSize.Height;
            for (int i = 0; i < items.Count; i++)
            {
                Rectangle r = RectFor(i);
                if (r.Bottom < 0) continue;
                if (r.Top > h) break;
                DrawCard(g, i, r);
            }
        }

        private void DrawCard(Graphics g, int i, Rectangle r)
        {
            MovieSummary m = items[i];
            bool sel = (i == selectedIndex);
            bool hov = (i == hoverIndex);

            Color fill = sel ? Pal.RowSel : (hov ? Pal.RowHover : Pal.Panel);
            using (GraphicsPath path = Draw.Round(r, 8))
            using (SolidBrush b = new SolidBrush(fill)) g.FillPath(b, path);
            using (GraphicsPath path = Draw.Round(r, 8))
            using (Pen p = new Pen(sel ? Pal.Accent : Pal.Border, sel ? 1.6f : 1f)) g.DrawPath(p, path);

            Rectangle pr = new Rectangle(r.X + 5, r.Y + 5, r.Width - 10, CardH - 10 - 40);
            DrawPoster(g, m, pr);

            if (m.Status.Length > 0 && m.Status.Length <= 12)
                DrawPill(g, m.Status, pr.X + 4, pr.Y + 4, Pal.Accent, false);

            if (m.Score > 0)
                DrawPill(g, m.Score.ToString("0.0", CultureInfo.InvariantCulture),
                    pr.Right - 4, pr.Y + 4, Color.FromArgb(20, 22, 30), true);

            Rectangle tr = new Rectangle(r.X + 8, pr.Bottom + 3, r.Width - 16, 34);
            TextRenderer.DrawText(g, m.Title, Font, tr, sel ? Color.White : Pal.Text,
                TextFormatFlags.WordBreak | TextFormatFlags.Top | TextFormatFlags.NoPadding |
                TextFormatFlags.EndEllipsis);
        }

        private void DrawPoster(Graphics g, MovieSummary m, Rectangle area)
        {
            Bitmap bmp = null;
            if (m.Poster.Length > 0)
                lock (posterGate) posters.TryGetValue(m.Poster, out bmp);

            if (bmp != null)
            {
                double s = Math.Min((double)area.Width / bmp.Width, (double)area.Height / bmp.Height);
                int w = Math.Max(1, (int)Math.Round(bmp.Width * s));
                int h = Math.Max(1, (int)Math.Round(bmp.Height * s));
                Rectangle dst = new Rectangle(area.X + (area.Width - w) / 2, area.Y + (area.Height - h) / 2, w, h);
                g.InterpolationMode = InterpolationMode.HighQualityBicubic;
                g.DrawImage(bmp, dst);
            }
            else
            {
                // 占位：没有封面时也是一张海报，不显示破图
                using (LinearGradientBrush b = new LinearGradientBrush(area,
                    Color.FromArgb(38, 45, 71), Color.FromArgb(24, 29, 50), LinearGradientMode.Vertical))
                    g.FillRectangle(b, area);
                using (Font f = new Font("Microsoft YaHei UI", 26F, FontStyle.Bold))
                    TextRenderer.DrawText(g, m.Initial, f, area, Color.FromArgb(120, Pal.Muted),
                        TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
            }
        }

        private static void DrawPill(Graphics g, string text, int x, int y, Color color, bool alignRight)
        {
            using (Font f = new Font("Microsoft YaHei UI", 7.5F, FontStyle.Bold))
            {
                Size sz = TextRenderer.MeasureText(text, f);
                int w = sz.Width + 12, h = 18;
                if (alignRight) x -= w;
                Rectangle r = new Rectangle(x, y, w, h);
                using (GraphicsPath p = Draw.Round(r, 9))
                using (SolidBrush b = new SolidBrush(Color.FromArgb(210, color))) g.FillPath(b, p);
                TextRenderer.DrawText(g, text, f, r, Color.White,
                    TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
            }
        }

        protected override void OnResize(EventArgs e)
        {
            base.OnResize(e);
            ReloadAfterResize();
        }
    }

    // ============ 详情页封面（等比居中） ============
    internal class DetailPoster : Control
    {
        private Bitmap bmp;
        private string initial = "?";
        private int token;

        public DetailPoster()
        {
            this.DoubleBuffered = true;
            SetStyle(ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint |
                     ControlStyles.OptimizedDoubleBuffer | ControlStyles.ResizeRedraw, true);
        }

        public void Show(string url, string title)
        {
            bmp = null;
            initial = string.IsNullOrEmpty(title) ? "?" : title.Substring(0, 1);
            Invalidate();
            if (string.IsNullOrEmpty(url)) return;

            int t = ++token;
            ThreadPool.QueueUserWorkItem(delegate
            {
                Bitmap b = CoverCache.Get(url, 320);
                if (t != token || IsDisposed) return;
                bmp = b;
                try
                {
                    if (InvokeRequired) BeginInvoke(new MethodInvoker(Invalidate));
                    else Invalidate();
                }
                catch { }
            });
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            Graphics g = e.Graphics;
            g.SmoothingMode = SmoothingMode.AntiAlias;
            g.Clear(Pal.Panel);
            if (bmp != null)
            {
                double s = Math.Min((double)Width / bmp.Width, (double)Height / bmp.Height);
                int w = Math.Max(1, (int)Math.Round(bmp.Width * s));
                int h = Math.Max(1, (int)Math.Round(bmp.Height * s));
                g.InterpolationMode = InterpolationMode.HighQualityBicubic;
                g.DrawImage(bmp, new Rectangle((Width - w) / 2, (Height - h) / 2, w, h));
            }
            else
            {
                using (Font f = new Font("Microsoft YaHei UI", 30F, FontStyle.Bold))
                    TextRenderer.DrawText(g, initial, f, ClientRectangle, Color.FromArgb(110, Pal.Muted),
                        TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
            }
        }
    }

    // ============ 影视库视图（海报墙 + 详情面板） ============
    internal class MovieBrowseView : Panel
    {
        private const int DetailW = 344;
        private const int BarH = 66;
        private const int FootH = 52;
        private const int StatusH = 26;

        private readonly MoviePosterWall wall = new MoviePosterWall();
        private readonly Panel bar = new Panel();
        private readonly Panel detailPanel = new Panel();
        private readonly RichTextBox detailBox = new RichTextBox();
        private readonly DetailPoster detailPoster = new DetailPoster();
        private readonly Panel statusBar = new Panel();
        private readonly Label lblStatus = new Label();
        private readonly TextBox box = new TextBox();
        private readonly ComboBox catBox = new ComboBox();
        // 这几个按钮在 BuildBar / BuildDetail 里创建，不能标 readonly
        private RButton btnSearch, btnLatest, btnCopyKeyword, btnSearchMagnet, btnCopySummary;

        private string mode = "latest";
        private string catId = "movie";
        private int page = 1;
        private int totalPages;
        private bool loading;
        private long detailToken;
        private MovieDetail currentDetail;

        public int Count { get { return wall.Count; } }

        // 详情里点「搜索磁力」时，把关键词交回主窗口驱动种子搜索
        public event EventHandler<MovieDetail> MagnetSearchRequested;

        // 片单数量变化（加载完一页 / 新搜索）时通知主窗口刷新导航计数
        public event EventHandler ItemsChanged;

        public MovieBrowseView()
        {
            this.BackColor = Pal.Bg;
            this.Visible = false;

            BuildBar();
            BuildDetail();

            this.Controls.Add(wall);
            this.Controls.Add(detailPanel);
            this.Controls.Add(statusBar);
            this.Controls.Add(bar);

            wall.SetItems(null);
            wall.EmptyText = "点击「最新更新」开始浏览影视库";
            wall.Activate += delegate(object s, MovieSummary m) { OpenDetail(m); };
            wall.SelectionChanged += delegate { ShowSelectionHint(); };
            wall.NeedMore += delegate { LoadMore(); };

            this.Resize += delegate { LayoutChildren(); };
            LayoutChildren();
        }

        private void BuildBar()
        {
            bar.BackColor = Pal.Bg;

            box.Location = new Point(16, 18);
            box.Size = new Size(400, 30);
            box.Font = new Font("Microsoft YaHei UI", 10F);
            box.BorderStyle = BorderStyle.FixedSingle;
            box.BackColor = Pal.Panel;
            box.ForeColor = Pal.Text;
            box.KeyDown += delegate(object s, KeyEventArgs e) { if (e.KeyCode == Keys.Enter) DoSearch(); };
            bar.Controls.Add(box);

            catBox.Location = new Point(424, 18);
            catBox.Size = new Size(104, 30);
            catBox.DropDownStyle = ComboBoxStyle.DropDownList;
            catBox.BackColor = Pal.Panel;
            catBox.ForeColor = Pal.Text;
            catBox.FlatStyle = FlatStyle.Flat;
            catBox.Items.AddRange(new object[] { "电影", "剧集", "短剧" });
            catBox.SelectedIndex = 0;
            catBox.SelectedIndexChanged += delegate
            {
                catId = CatId(catBox.SelectedIndex);
                if (mode == "list") LoadPage(true);
            };
            bar.Controls.Add(catBox);

            btnSearch = new RButton("搜索", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            btnSearch.Location = new Point(536, 16);
            btnSearch.Size = new Size(84, 34);
            btnSearch.Click += delegate { DoSearch(); };
            bar.Controls.Add(btnSearch);

            btnLatest = new RButton("最新更新", Pal.Panel, Pal.RowHover, Pal.RowSel, Pal.Text);
            btnLatest.Location = new Point(628, 16);
            btnLatest.Size = new Size(96, 34);
            btnLatest.Click += delegate { LoadLatest(); };
            bar.Controls.Add(btnLatest);

            // 状态信息放到底部整条：顶部按钮区在窄窗口下会挤掉文字
            statusBar.BackColor = Pal.Panel;
            statusBar.Paint += delegate(object s2, PaintEventArgs e)
            {
                using (Pen pen = new Pen(Pal.Border)) e.Graphics.DrawLine(pen, 0, 0, statusBar.Width, 0);
            };

            lblStatus.Text = "点击「最新更新」开始浏览影视库";
            lblStatus.ForeColor = Pal.Muted;
            lblStatus.Font = new Font("Microsoft YaHei UI", 8.5F);
            lblStatus.AutoSize = true;
            lblStatus.Location = new Point(16, 5);
            statusBar.Controls.Add(lblStatus);
        }

        private void BuildDetail()
        {
            detailPanel.BackColor = Pal.Panel;
            detailPanel.Visible = false;
            detailPanel.Paint += delegate(object s, PaintEventArgs e)
            {
                using (Pen pen = new Pen(Pal.Border))
                    e.Graphics.DrawLine(pen, 0, 0, 0, detailPanel.Height);
            };

            detailPoster.Location = new Point(12, 12);
            detailPoster.Size = new Size(DetailW - 24, 190);
            detailPanel.Controls.Add(detailPoster);

            detailBox.Location = new Point(12, 212);
            detailBox.Size = new Size(DetailW - 24, 200);
            detailBox.ReadOnly = true;
            detailBox.BorderStyle = BorderStyle.None;
            detailBox.BackColor = Pal.Panel;
            detailBox.ForeColor = Pal.Text;
            detailBox.ScrollBars = RichTextBoxScrollBars.Vertical;
            detailBox.DetectUrls = false;
            detailPanel.Controls.Add(detailBox);

            Panel foot = new Panel();
            foot.Dock = DockStyle.Bottom;
            foot.Height = FootH;
            foot.BackColor = Pal.Panel;
            foot.Paint += delegate(object s, PaintEventArgs e)
            {
                using (Pen pen = new Pen(Pal.Border)) e.Graphics.DrawLine(pen, 0, 0, foot.Width, 0);
            };

            btnCopyKeyword = new RButton("复制关键词", Pal.Panel, Pal.RowHover, Pal.RowSel, Pal.Text);
            btnCopyKeyword.Location = new Point(12, 10);
            btnCopyKeyword.Size = new Size(100, 32);
            btnCopyKeyword.Click += delegate { CopyKeyword(); };
            foot.Controls.Add(btnCopyKeyword);

            btnSearchMagnet = new RButton("搜索磁力", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            btnSearchMagnet.Location = new Point(120, 10);
            btnSearchMagnet.Size = new Size(100, 32);
            btnSearchMagnet.Click += delegate { SearchMagnet(); };
            foot.Controls.Add(btnSearchMagnet);

            btnCopySummary = new RButton("复制简介", Pal.Panel, Pal.RowHover, Pal.RowSel, Pal.Text);
            btnCopySummary.Location = new Point(228, 10);
            btnCopySummary.Size = new Size(100, 32);
            btnCopySummary.Click += delegate { CopySummary(); };
            foot.Controls.Add(btnCopySummary);

            detailPanel.Controls.Add(foot);
        }

        private void LayoutChildren()
        {
            int w = ClientSize.Width, h = ClientSize.Height;
            bar.SetBounds(0, 0, w, BarH);
            statusBar.SetBounds(0, Math.Max(0, h - StatusH), w, StatusH);
            int dw = detailPanel.Visible ? DetailW : 0;
            int bodyH = Math.Max(0, h - BarH - StatusH);
            if (dw > 0) detailPanel.SetBounds(w - DetailW, BarH, DetailW, bodyH);
            wall.SetBounds(0, BarH, Math.Max(0, w - dw), bodyH);
            if (dw > 0)
            {
                detailPoster.SetBounds(12, 12, DetailW - 24, 190);
                detailBox.SetBounds(12, 212, DetailW - 24, Math.Max(40, bodyH - 212 - FootH - 8));
            }
        }

        // ============ 列表加载 ============

        private static string CatId(int index)
        {
            if (index == 2) return "short";
            if (index == 1) return "tv";
            return "movie";
        }

        private static string CatLabel(string cat)
        {
            if (cat == "tv") return "剧集";
            if (cat == "short") return "短剧";
            if (cat == "search") return "搜索结果";
            return "电影";
        }

        public void LoadLatest()
        {
            if (loading) return;
            loading = true;
            mode = "latest";
            page = 1;
            totalPages = 0;
            CloseDetail();
            wall.EmptyText = "正在加载最新更新…";
            wall.SetItems(null);
            RunBg(delegate
            {
                MoviePage p = DyttApi.Latest(60);
                RunOnUi(delegate
                {
                    loading = false;
                    wall.EmptyText = "没有取到内容，稍后重试";
                    wall.SetItems(p.Items);
                    if (p.Stale)
                    {
                        // 服务端拿旧缓存顶上的：必须显式告知，否则用户会以为站点不更新了。
                        SetHint("最新更新 · " + p.Items.Count + " 部 · " +
                            (p.Hint.Length > 0 ? p.Hint : "内容源暂时不可用，显示的是上次抓取的片单"));
                        wall.EmptyText = "内容源暂时不可用，显示上次抓取的片单";
                    }
                    else
                    {
                        SetHint("最新更新 · " + p.Items.Count + " 部");
                        if (p.Hint.Length > 0) SetHint("最新更新 · " + p.Items.Count + " 部 · " + p.Hint);
                    }
                    if (ItemsChanged != null) ItemsChanged(this, EventArgs.Empty);
                    Logger.Op("影视库：加载最新更新 " + p.Items.Count + " 部" +
                        (p.Stale ? "（回退旧缓存）" : ""));
                });
            }, Fail("加载最新更新失败"));
        }

        public void LoadCategory(string cat)
        {
            catId = cat;
            mode = "list";
            if (cat == "movie" && catBox.SelectedIndex != 0) catBox.SelectedIndex = 0;
            else if (cat == "tv" && catBox.SelectedIndex != 1) catBox.SelectedIndex = 1;
            else if (cat == "short" && catBox.SelectedIndex != 2) catBox.SelectedIndex = 2;
            LoadPage(true);
        }

        private void LoadPage(bool reset)
        {
            if (loading) return;
            loading = true;
            if (reset) { page = 1; totalPages = 0; wall.SetItems(null); }
            wall.EmptyText = "正在加载…";
            int want = page;
            string cat = catId;
            RunBg(delegate
            {
                MoviePage p = DyttApi.ListPage(cat, want);
                RunOnUi(delegate
                {
                    loading = false;
                    totalPages = p.TotalPages;
                    page = p.Page + 1;
                    if (want <= 1) wall.SetItems(p.Items); else wall.AppendItems(p.Items);
                    if (wall.Count == 0) wall.EmptyText = "该分类暂无内容";
                    SetHint(CatLabel(cat) + " · 第 " + p.Page + "/" + p.TotalPages + " 页 · 共 " + wall.Count + " 部");
                    if (ItemsChanged != null) ItemsChanged(this, EventArgs.Empty);
                });
            }, Fail("加载片单失败"));
        }

        private void LoadMore()
        {
            if (loading || mode != "list") return;
            if (totalPages > 0 && page > totalPages) return;
            loading = true;
            int want = page;
            string cat = catId;
            RunBg(delegate
            {
                MoviePage p = DyttApi.ListPage(cat, want);
                RunOnUi(delegate
                {
                    loading = false;
                    if (p.Items.Count == 0) return;
                    totalPages = p.TotalPages;
                    page = p.Page + 1;
                    wall.AppendItems(p.Items);
                    SetHint(CatLabel(cat) + " · 第 " + p.Page + "/" + p.TotalPages + " 页 · 共 " + wall.Count + " 部");
                });
            }, delegate(Exception ex)
            {
                RunOnUi(delegate { loading = false; Logger.App("加载下一页失败：" + ex.Message); });
            });
        }

        private void DoSearch()
        {
            string q = (box.Text ?? "").Trim();
            if (q.Length == 0) { MessageBox.Show(this, "请输入片名关键词。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information); return; }
            if (loading) return;
            SearchKeyword(q);
        }

        // 供主窗口从「搜索」页跳进来复用
        public void SearchKeyword(string q)
        {
            if (string.IsNullOrEmpty(q)) return;
            if (loading) return;
            loading = true;
            mode = "search";
            box.Text = q;
            CloseDetail();
            wall.EmptyText = "正在搜索「" + q + "」…";
            wall.SetItems(null);
            RunBg(delegate
            {
                MoviePage p = DyttApi.Search(q);
                RunOnUi(delegate
                {
                    loading = false;
                    wall.EmptyText = "没有找到「" + q + "」，换个关键词试试";
                    wall.SetItems(p.Items);
                    SetHint("搜索「" + q + "」· " + p.Items.Count + " 部");
                    if (ItemsChanged != null) ItemsChanged(this, EventArgs.Empty);
                    Logger.Op("影视库：搜索「" + q + "」" + p.Items.Count + " 部");
                });
            }, Fail("搜索失败"));
        }

        private void SetHint(string text)
        {
            lblStatus.Text = text == null ? "" : text;
        }

        private void ShowSelectionHint()
        {
            MovieSummary m = wall.Selected;
            if (m != null) SetHint("已选《" + m.Title + "》· 双击查看简介");
        }

        public void FocusSearch()
        {
            try { box.Focus(); } catch { }
        }

        // ============ 详情 ============

        private void OpenDetail(MovieSummary m)
        {
            if (m == null) return;
            long t = ++detailToken;
            SetHint("正在加载《" + m.Title + "》…");
            RunBg(delegate
            {
                MovieDetail d = DyttApi.Detail(m.Id.ToString(CultureInfo.InvariantCulture));
                RunOnUi(delegate
                {
                    if (t != detailToken) return;
                    ShowDetail(d);
                });
            }, Fail("加载详情失败"));
        }

        private void ShowDetail(MovieDetail d)
        {
            currentDetail = d;
            detailPanel.Visible = true;
            LayoutChildren();

            detailPoster.Show(d.Poster, d.Title);
            try
            {
                // 用纯文本而非 RTF：RichTextBox.Rtf 在句柄尚未创建时会抛异常，
                // 结果是详情区静默空白；纯文本没这个坑。
                detailBox.Text = BuildPlainText(d);
                detailBox.SelectionStart = 0;
                detailBox.ScrollToCaret();
            }
            catch (Exception ex)
            {
                Logger.App("详情渲染失败：" + ex);
                detailBox.Text = d.Title;
            }
            SetHint("《" + d.Title + "》" + (d.Year > 0 ? d.Year + " · " : "") + d.Summary.Trim().Length + " 字简介");
            Logger.Op("影视库：查看《" + d.Title + "》(" + d.Id + ")");
        }

        public void CloseDetail()
        {
            detailToken++;
            detailPanel.Visible = false;
            currentDetail = null;
            LayoutChildren();
            try { wall.Focus(); } catch { }
        }

        private void CopyKeyword()
        {
            if (currentDetail == null) return;
            SetClipboard(currentDetail.Keyword);
            MessageBox.Show(this, "已复制检索关键词：" + currentDetail.Keyword + "\n可直接粘贴到「搜索」里找资源。",
                "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
        }

        private void CopySummary()
        {
            if (currentDetail == null) return;
            MovieDetail d = currentDetail;
            StringBuilder sb = new StringBuilder();
            sb.AppendLine(d.Title + (d.Year > 0 ? "（" + d.Year + "）" : ""));
            if (d.MetaLine().Length > 0) sb.AppendLine(d.MetaLine());
            if (d.Actors.Count > 0) sb.AppendLine("主演：" + string.Join("、", d.Actors.ToArray()));
            if (d.Summary.Trim().Length > 0) sb.AppendLine().AppendLine(d.Summary.Trim());
            if (d.MagnetQueries.Count > 0) sb.AppendLine().AppendLine("检索关键词：" + string.Join(" / ", d.MagnetQueries.ToArray()));
            SetClipboard(sb.ToString().Trim());
            MessageBox.Show(this, "已复制《" + d.Title + "》的简介。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
        }

        private void SetClipboard(string text)
        {
            try { Clipboard.SetText(text); }
            catch (Exception ex) { Logger.App("复制到剪贴板失败：" + ex.Message); }
        }

        private void SearchMagnet()
        {
            if (currentDetail == null) return;
            if (MagnetSearchRequested != null) MagnetSearchRequested(this, currentDetail);
        }

        private Action<Exception> Fail(string prefix)
        {
            return delegate(Exception ex)
            {
                RunOnUi(delegate
                {
                    loading = false;
                    SetHint(prefix);
                    MessageBox.Show(this, prefix + "：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
                });
            };
        }

        private void RunBg(Action work, Action<Exception> onError)
        {
            ThreadPool.QueueUserWorkItem(delegate
            {
                try { work(); }
                catch (Exception ex) { if (onError != null) RunOnUi(delegate { onError(ex); }); }
            });
        }

        private void RunOnUi(Action a)
        {
            if (IsDisposed || Disposing) return;
            if (InvokeRequired) { try { BeginInvoke(new MethodInvoker(delegate { a(); })); } catch { } }
            else a();
        }
        // ============ 详情文本 ============

        // 纯文本排版：标题 + 元信息 + 分段正文，
        // 用 RichTextBox 显示（可滚动、可选中复制）。
        private static string BuildPlainText(MovieDetail d)
        {
            StringBuilder sb = new StringBuilder();
            sb.AppendLine(d.Title + (d.Year > 0 ? "（" + d.Year + "）" : ""));
            if (d.MetaLine().Length > 0) sb.AppendLine(d.MetaLine());

            List<string> bits = new List<string>();
            if (d.Score > 0) bits.Add("评分 " + d.Score.ToString("0.0", CultureInfo.InvariantCulture));
            if (d.EpisodeCount > 0) bits.Add("共 " + d.EpisodeCount + " 集");
            if (d.FirstAired.Length > 0) bits.Add("首播 " + d.FirstAired);
            if (bits.Count > 0) sb.AppendLine(string.Join(" · ", bits.ToArray()));

            sb.AppendLine();
            sb.AppendLine("【简介】");
            sb.AppendLine(d.Summary.Trim().Length > 0 ? d.Summary.Trim() : "暂无简介");

            if (d.Actors.Count > 0)
            {
                sb.AppendLine();
                sb.AppendLine("【主演】");
                sb.AppendLine(string.Join("、", d.Actors.ToArray()));
            }
            if (d.Genres.Count > 0)
            {
                sb.AppendLine();
                sb.AppendLine("【类型】");
                sb.AppendLine(string.Join(" / ", d.Genres.ToArray()));
            }
            if (d.PlaySources.Count > 0)
            {
                sb.AppendLine();
                sb.AppendLine("【在线线路】");
                sb.AppendLine(string.Join("、", d.PlaySources.ToArray()));
            }
            if (d.MagnetQueries.Count > 0)
            {
                sb.AppendLine();
                sb.AppendLine("【磁力检索关键词】");
                foreach (string q in d.MagnetQueries) sb.AppendLine(q);
                sb.Append("（源站只提供在线播放，没有磁力链接；用上面的关键词去「搜索」里找资源。）");
            }
            if (d.UpdatedAt.Length > 0)
            {
                sb.AppendLine();
                sb.Append("更新于 " + d.UpdatedAt);
            }
            return sb.ToString().TrimEnd();
        }
    }
}
