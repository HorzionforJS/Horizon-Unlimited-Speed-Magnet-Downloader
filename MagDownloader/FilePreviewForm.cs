using System;
using System.Collections.Generic;
using System.Drawing;
using System.Threading;
using System.Windows.Forms;

namespace MagDownloader
{
    // ============ 单个文件条目（aria2.getFiles 结果） ============
    internal class FileEntry
    {
        public int Index;
        public string Path;
        public long Length, Completed;
        public bool Selected;

        public string DisplayPath { get { return Path == null ? "" : Path.Replace('/', '\\'); } }
    }

    // ============ 磁力链接在线预览（文件列表 + 选择性下载） ============
    internal class FilePreviewForm : Form
    {
        private Aria2Client aria;
        private string gid;
        private bool isBt;
        private ListView lv;
        private Label lblStatus;
        private List<FileEntry> entries;
        private System.Windows.Forms.Timer timer;
        private RButton btnGo, btnAll, btnNone;
        private int waited;
        private bool resolved;
        private bool resolving;

        public FilePreviewForm(Aria2Client client, string gid)
        {
            this.aria = client;
            this.gid = gid;

            this.Text = "在线预览磁力内容";
            this.FormBorderStyle = FormBorderStyle.FixedDialog;
            this.StartPosition = FormStartPosition.CenterParent;
            this.MaximizeBox = false;
            this.MinimizeBox = false;
            this.ShowInTaskbar = false;
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);
            this.ClientSize = new Size(620, 466);

            lblStatus = new Label();
            lblStatus.Text = "正在解析磁力链接元数据，请稍候…（需要可用的 BT / DHT 节点）";
            lblStatus.ForeColor = Pal.Muted;
            lblStatus.Location = new Point(16, 12);
            lblStatus.AutoSize = true;
            this.Controls.Add(lblStatus);

            lv = new ListView();
            lv.Location = new Point(16, 36);
            lv.Size = new Size(588, 356);
            lv.View = View.Details;
            lv.FullRowSelect = true;
            lv.GridLines = true;
            lv.CheckBoxes = true;
            lv.BackColor = Color.FromArgb(18, 22, 38);
            lv.ForeColor = Pal.Text;
            lv.Columns.Add("文件", 440);
            lv.Columns.Add("大小", 140);
            this.Controls.Add(lv);

            btnAll = MakeBtn("全选", 16, 402);
            btnAll.Enabled = false;
            btnAll.Click += delegate { SetAll(true); };
            this.Controls.Add(btnAll);

            btnNone = MakeBtn("全不选", 100, 402);
            btnNone.Enabled = false;
            btnNone.Click += delegate { SetAll(false); };
            this.Controls.Add(btnNone);

            btnGo = new RButton("开始下载所选", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            btnGo.Location = new Point(408, 398);
            btnGo.Size = new Size(126, 40);
            btnGo.Radius = 12;
            btnGo.Enabled = false;
            btnGo.Click += delegate { DoGo(); };
            this.Controls.Add(btnGo);

            RButton btnClose = MakeBtn("关闭", 540, 402);
            btnClose.Click += delegate { this.Close(); };
            this.Controls.Add(btnClose);

            this.FormClosed += delegate(object s, FormClosedEventArgs e)
            {
                if (timer != null) { timer.Stop(); timer.Dispose(); timer = null; }
            };

            timer = new System.Windows.Forms.Timer();
            timer.Interval = 1000;
            timer.Tick += delegate { CheckOnce(); };

            this.Shown += delegate(object s, EventArgs e)
            {
                ThreadPool.QueueUserWorkItem(delegate { try { aria.Unpause(gid); } catch { } });
                CheckOnce();
                timer.Start();
            };
        }

        private RButton MakeBtn(string text, int x, int y)
        {
            RButton b = new RButton(text, Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            b.Location = new Point(x, y);
            b.Size = new Size(80, 32);
            b.Radius = 10;
            return b;
        }

        private void CheckOnce()
        {
            if (resolved || resolving) return;
            resolving = true;
            ThreadPool.QueueUserWorkItem(delegate
            {
                TaskInfo t = null;
                List<FileEntry> files = new List<FileEntry>();
                try { t = aria.Status(gid); files = aria.GetFiles(gid); }
                catch { }
                if (this.IsDisposed || this.Disposing) return;
                try { this.BeginInvoke(new MethodInvoker(delegate { FinishCheck(t, files); resolving = false; })); }
                catch { resolving = false; }
            });
        }

        private void FinishCheck(TaskInfo t, List<FileEntry> files)
        {
            if (resolved) return;
            waited++;

            if (t == null)
            {
                ShowTimeout("任务不存在或已被移除。");
                return;
            }

            bool ready = t.TotalLength > 0 && files.Count > 0 &&
                         !(files.Count == 1 && files[0].Path != null &&
                           files[0].Path.IndexOf("[METADATA]", StringComparison.Ordinal) == 0);

            if (ready)
            {
                resolved = true;
                timer.Stop();
                isBt = t.IsBt;
                ThreadPool.QueueUserWorkItem(delegate { try { aria.ForcePause(gid); } catch { } });

                entries = new List<FileEntry>();
                entries.AddRange(files);
                entries.Sort(delegate(FileEntry a, FileEntry b) { return a.Index.CompareTo(b.Index); });

                lv.BeginUpdate();
                lv.Items.Clear();
                foreach (FileEntry f in entries)
                {
                    ListViewItem it = new ListViewItem(f.DisplayPath);
                    it.SubItems.Add(f.Length > 0 ? FmtSize(f.Length) : "未知");
                    it.Checked = true;
                    lv.Items.Add(it);
                }
                lv.EndUpdate();

                lblStatus.Text = "共 " + entries.Count + " 个文件，勾选要下载的文件后点「开始下载所选」：";
                lblStatus.ForeColor = Pal.Text;
                btnAll.Enabled = true;
                btnNone.Enabled = true;
                btnGo.Enabled = true;
            }
            else if (waited >= 90)
            {
                ShowTimeout("解析超时：未能获取磁力元数据（可能网络受限或无可用节点）。可关闭后稍后再试。");
            }
        }

        private void ShowTimeout(string msg)
        {
            resolved = true;
            if (timer != null) timer.Stop();
            lblStatus.Text = msg;
            lblStatus.ForeColor = Pal.Danger;
            btnGo.Enabled = false;
        }

        private void SetAll(bool v)
        {
            for (int i = 0; i < lv.Items.Count; i++) lv.Items[i].Checked = v;
        }

        private void DoGo()
        {
            if (entries == null) return;
            List<int> idx = new List<int>();
            for (int i = 0; i < lv.Items.Count; i++)
                if (lv.Items[i].Checked && i < entries.Count) idx.Add(entries[i].Index);

            if (idx.Count == 0)
            {
                MessageBox.Show(this, "请至少勾选一个文件。", "提示", MessageBoxButtons.OK, MessageBoxIcon.Information);
                return;
            }

            int n = idx.Count;
            int total = entries.Count;
            ThreadPool.QueueUserWorkItem(delegate
            {
                try
                {
                    if (isBt && idx.Count < total)
                    {
                        List<string> s = idx.ConvertAll(delegate(int x) { return x.ToString(); });
                        string mask = string.Join(",", s.ToArray());
                        aria.ChangeOption(gid, new Dictionary<string, object> { { "select-file", mask } });
                    }
                    aria.Unpause(gid);
                    Logger.Op("预览选择下载：" + gid + " 勾选 " + n + "/" + total + " 个文件");
                    if (this.IsDisposed || this.Disposing) return;
                    this.BeginInvoke(new MethodInvoker(delegate
                    {
                        MessageBox.Show(this, "已开始下载所选文件。", "完成", MessageBoxButtons.OK, MessageBoxIcon.Information);
                        this.DialogResult = DialogResult.OK;
                        this.Close();
                    }));
                }
                catch (Exception ex)
                {
                    if (this.IsDisposed || this.Disposing) return;
                    this.BeginInvoke(new MethodInvoker(delegate
                    {
                        MessageBox.Show(this, "开始下载失败：" + ex.Message, "错误", MessageBoxButtons.OK, MessageBoxIcon.Error);
                    }));
                }
            });
        }

        private static string FmtSize(long b)
        {
            if (b <= 0) return "-";
            double v = b;
            string[] u = new string[] { "B", "KB", "MB", "GB", "TB" };
            int i = 0;
            while (v >= 1024 && i < u.Length - 1) { v /= 1024; i++; }
            return v.ToString(i == 0 ? "0" : "0.0") + " " + u[i];
        }
    }
}