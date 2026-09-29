using System;
using System.Drawing;
using System.IO;
using System.Net;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Windows.Forms;

namespace MagDownloader
{
    // ============ 通用单列输入对话框 ============
    internal class InputForm : Form
    {
        private TextBox[] boxes;
        public string[] Values;

        public InputForm(string title, string[] labels, bool[] isPassword)
        {
            this.Text = title;
            this.FormBorderStyle = FormBorderStyle.FixedDialog;
            this.StartPosition = FormStartPosition.CenterParent;
            this.MaximizeBox = false;
            this.MinimizeBox = false;
            this.ShowInTaskbar = false;
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);

            int n = labels.Length;
            boxes = new TextBox[n];
            int y = 18;
            for (int i = 0; i < n; i++)
            {
                Label l = new Label();
                l.Text = labels[i];
                l.ForeColor = Pal.Text;
                l.Location = new Point(20, y);
                l.AutoSize = true;
                this.Controls.Add(l);

                TextBox t = new TextBox();
                t.Location = new Point(20, y + 22);
                t.Size = new Size(300, 30);
                t.Font = new Font("Microsoft YaHei UI", 10F);
                t.BorderStyle = BorderStyle.FixedSingle;
                t.BackColor = Color.FromArgb(26, 32, 56);
                t.ForeColor = Pal.Text;
                if (isPassword[i]) t.UseSystemPasswordChar = true;
                boxes[i] = t;
                this.Controls.Add(t);
                y += 64;
            }

            RButton ok = new RButton("确 定", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            ok.Location = new Point(130, y + 8);
            ok.Size = new Size(90, 32);
            ok.Radius = 10;
            ok.Click += delegate { OnOk(); };
            this.Controls.Add(ok);

            RButton cancel = new RButton("取 消", Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            cancel.Location = new Point(230, y + 8);
            cancel.Size = new Size(90, 32);
            cancel.Radius = 10;
            cancel.Click += delegate { this.Close(); };
            this.Controls.Add(cancel);

            this.ClientSize = new Size(340, y + 58);
            this.KeyPreview = true;
            this.KeyDown += delegate(object s, KeyEventArgs e)
            {
                if (e.KeyCode == Keys.Escape) this.Close();
            };
        }

        private void OnOk()
        {
            Values = new string[boxes.Length];
            for (int i = 0; i < boxes.Length; i++) Values[i] = boxes[i].Text;
            this.DialogResult = DialogResult.OK;
            this.Close();
        }
    }

    // ============ 云端服务器设置对话框 ============
    //
    // 客户端不再内置任何服务器地址，用户在这里填一次，地址写进
    // %LOCALAPPDATA%\MagDownloader\data\cloud.cfg，下次启动自动读回。
    //
    // 「测试连接」走 /healthz：它不需要登录，能最直接地回答「这个地址是不是一台
    // 地平线服务端」，同时把服务端版本号带回来给用户核对。
    internal class CloudSettingsForm : Form
    {
        private TextBox box;
        private Label lblState;
        private RButton btnTest;

        public CloudSettingsForm()
        {
            this.Text = "云端服务器设置";
            this.FormBorderStyle = FormBorderStyle.FixedDialog;
            this.StartPosition = FormStartPosition.CenterParent;
            this.MaximizeBox = false;
            this.MinimizeBox = false;
            this.ShowInTaskbar = false;
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);

            Label l1 = new Label();
            l1.Text = "服务器地址（不填 = 本地模式，影视库与云端加速不可用）";
            l1.ForeColor = Pal.Text;
            l1.Location = new Point(20, 18);
            l1.AutoSize = true;
            this.Controls.Add(l1);

            box = new TextBox();
            box.Location = new Point(20, 40);
            box.Size = new Size(400, 30);
            box.Font = new Font("Microsoft YaHei UI", 10F);
            box.BorderStyle = BorderStyle.FixedSingle;
            box.BackColor = Color.FromArgb(26, 32, 56);
            box.ForeColor = Pal.Text;
            box.Text = CloudConfig.ServerUrl;
            this.Controls.Add(box);

            Label l2 = new Label();
            l2.Text = "例：http://192.168.1.10:8080 或 https://your-domain.com";
            l2.ForeColor = Pal.Muted;
            l2.Font = new Font("Microsoft YaHei UI", 8.5F);
            l2.Location = new Point(20, 74);
            l2.AutoSize = true;
            this.Controls.Add(l2);

            btnTest = new RButton("测试连接", Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            btnTest.Location = new Point(20, 100);
            btnTest.Size = new Size(110, 32);
            btnTest.Radius = 10;
            btnTest.Click += delegate { TestAsync(); };
            this.Controls.Add(btnTest);

            lblState = new Label();
            lblState.Location = new Point(140, 106);
            lblState.Size = new Size(280, 40);
            lblState.ForeColor = Pal.Muted;
            lblState.Font = new Font("Microsoft YaHei UI", 8.5F);
            this.Controls.Add(lblState);

            RButton ok = new RButton("保 存", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            ok.Location = new Point(230, 152);
            ok.Size = new Size(90, 34);
            ok.Radius = 10;
            ok.Click += delegate { OnSave(); };
            this.Controls.Add(ok);

            RButton cancel = new RButton("取 消", Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            cancel.Location = new Point(330, 152);
            cancel.Size = new Size(90, 34);
            cancel.Radius = 10;
            cancel.Click += delegate { this.Close(); };
            this.Controls.Add(cancel);

            this.ClientSize = new Size(440, 202);
            // RButton 继承自 Control 而不是 Button，没有 IButtonControl，
            // 所以不能用 AcceptButton 绑回车——自己拦一下 KeyDown 即可。
            this.KeyPreview = true;
            this.KeyDown += delegate(object s, KeyEventArgs e)
            {
                if (e.KeyCode == Keys.Escape) { e.Handled = true; this.Close(); }
                else if (e.KeyCode == Keys.Enter) { e.Handled = true; OnSave(); }
            };
        }

        // 地址规整：补全 scheme、去掉尾部斜杠与空格。
        // 用户十有八九只填「1.2.3.4:8080」，直接存下来的话 WebRequest.Create 会抛
        // 「不支持 URI 格式」，报错离原因太远。
        private string Normalize()
        {
            string u = (box.Text ?? "").Trim();
            if (u.Length == 0) return "";
            if (u.IndexOf("://", StringComparison.Ordinal) < 0) u = "http://" + u;
            return u.TrimEnd('/');
        }

        private void TestAsync()
        {
            string url = Normalize();
            if (url.Length == 0) { SetState("请输入服务器地址，或留空以使用本地模式。", Pal.Warning); return; }

            btnTest.Enabled = false;
            SetState("正在连接 " + url + " …", Pal.Muted);

            ThreadPool.QueueUserWorkItem(delegate
            {
                string err = null, version = "";
                try
                {
                    HttpWebRequest req = (HttpWebRequest)WebRequest.Create(url + "/healthz");
                    req.Method = "GET";
                    req.Timeout = 8000;
                    req.ReadWriteTimeout = 8000;
                    using (HttpWebResponse resp = (HttpWebResponse)req.GetResponse())
                    using (StreamReader sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                        version = ParseVersion(sr.ReadToEnd());
                }
                catch (Exception ex) { err = ex.Message; }

                if (this.IsDisposed || this.Disposing) return;
                try
                {
                    this.BeginInvoke(new MethodInvoker(delegate
                    {
                        btnTest.Enabled = true;
                        if (err != null) SetState("连接失败：" + err, Pal.Danger);
                        else if (version.Length > 0)
                            SetState("连接成功，服务端版本 " + version
                                + (version == AppVersion.Number ? "（与本客户端一致）" : "（与客户端版本不一致，建议一并升级）"),
                                version == AppVersion.Number ? Pal.Success : Pal.Warning);
                        else
                            SetState("已连上该地址，但它没有返回服务端版本——可能不是地平线服务端。", Pal.Warning);
                    }));
                }
                catch { }
            });
        }

        // 不引 JavaScriptSerializer：这里只需要一个字段，正则够了。
        private static string ParseVersion(string json)
        {
            if (string.IsNullOrEmpty(json)) return "";
            Match m = Regex.Match(json, @"""version""\s*:\s*""([^""]*)""");
            return m.Success ? m.Groups[1].Value : "";
        }

        private void SetState(string text, Color color)
        {
            lblState.Text = text;
            lblState.ForeColor = color;
        }

        private void OnSave()
        {
            string url = Normalize();
            if (url.Length > 0 && url.IndexOf("://", StringComparison.Ordinal) < 0)
            {
                SetState("地址格式不对，请以 http:// 或 https:// 开头。", Pal.Danger);
                return;
            }
            CloudConfig.Save(url);
            this.DialogResult = DialogResult.OK;
            this.Close();
        }
    }

    // ============ 服务器设置入口（登录页 / 主界面共用） ============
    internal static class CloudSettingsUI
    {
        // 返回 true 表示用户点了保存。
        public static bool Open(IWin32Window owner)
        {
            CloudConfig.Load();
            using (CloudSettingsForm f = new CloudSettingsForm())
                return f.ShowDialog(owner) == DialogResult.OK;
        }
    }

    // ============ 账号相关操作（修改密码 / 用户管理） ============
    internal static class AccountUI
    {
        public static void ChangePassword(IWin32Window owner)
        {
            Control ctrl = owner as Control;
            using (InputForm f = new InputForm("修改密码", new string[] { "原密码", "新密码", "确认新密码" }, new bool[] { true, true, true }))
            {
                if (f.ShowDialog(owner) == DialogResult.OK)
                {
                    string oldPwd = f.Values[0];
                    string newPwd = f.Values[1];
                    string confirmPwd = f.Values[2];
                    if (newPwd.Length < 6) { Msg(owner, "新密码长度至少 6 位"); return; }
                    if (newPwd != confirmPwd) { Msg(owner, "两次输入的新密码不一致"); return; }

                    // PBKDF2 校验 + 重置（各约百毫秒）与写库放到后台，避免界面卡顿
                    ThreadPool.QueueUserWorkItem(delegate
                    {
                        string err;
                        if (!DataStore.VerifyPassword(Session.User, oldPwd)) err = "原密码错误";
                        else err = DataStore.ResetPassword(Session.User, newPwd);
                        if (err == null) Logger.Op("修改密码：" + Session.User);

                        if (ctrl == null || ctrl.IsDisposed || ctrl.Disposing) return;
                        try
                        {
                            ctrl.BeginInvoke(new MethodInvoker(delegate
                            {
                                if (err != null) Msg(owner, err);
                                else Msg(owner, "密码修改成功", MessageBoxIcon.Information);
                            }));
                        }
                        catch { }
                    });
                }
            }
        }

        public static void UserManage(IWin32Window owner)
        {
            using (UserAdminForm f = new UserAdminForm()) f.ShowDialog(owner);
        }

        private static void Msg(IWin32Window o, string s, MessageBoxIcon ic = MessageBoxIcon.Error)
        {
            MessageBox.Show(o, s, "地平线磁力下载", MessageBoxButtons.OK, ic);
        }
    }

    // ============ 用户管理窗口（管理员） ============
    internal class UserAdminForm : Form
    {
        private ListView lv;

        public UserAdminForm()
        {
            this.Text = "用户管理";
            this.FormBorderStyle = FormBorderStyle.FixedDialog;
            this.StartPosition = FormStartPosition.CenterParent;
            this.MaximizeBox = false;
            this.MinimizeBox = false;
            this.ShowInTaskbar = false;
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);

            lv = new ListView();
            lv.Location = new Point(16, 16);
            lv.Size = new Size(528, 320);
            lv.View = View.Details;
            lv.FullRowSelect = true;
            lv.GridLines = true;
            lv.BackColor = Color.FromArgb(18, 22, 38);
            lv.ForeColor = Pal.Text;
            lv.Columns.Add("用户名", 120);
            lv.Columns.Add("角色", 70);
            lv.Columns.Add("最后登录", 170);
            lv.Columns.Add("创建时间", 155);
            this.Controls.Add(lv);

            RButton btnAdd = MakeBtn("新增用户", 16);
            btnAdd.Click += delegate { DoAdd(); };
            this.Controls.Add(btnAdd);

            RButton btnDel = MakeBtn("删除选中", 112);
            btnDel.Click += delegate { DoDelete(); };
            this.Controls.Add(btnDel);

            RButton btnReset = MakeBtn("重置密码", 208);
            btnReset.Click += delegate { DoReset(); };
            this.Controls.Add(btnReset);

            RButton btnClose = MakeBtn("关 闭", 448);
            btnClose.Click += delegate { this.Close(); };
            this.Controls.Add(btnClose);

            this.ClientSize = new Size(560, 402);
            RefreshList();
        }

        private RButton MakeBtn(string text, int x)
        {
            RButton b = new RButton(text, Pal.Panel, Color.FromArgb(26, 32, 56), Color.FromArgb(33, 41, 70), Pal.Text);
            b.Location = new Point(x, 350);
            b.Size = new Size(88, 32);
            b.Radius = 10;
            return b;
        }

        private void RefreshList()
        {
            lv.Items.Clear();
            foreach (UserAccount a in DataStore.All())
            {
                ListViewItem it = new ListViewItem(a.User);
                it.SubItems.Add(a.Role == "admin" ? "管理员" : "普通用户");
                it.SubItems.Add(string.IsNullOrEmpty(a.LastLogin) ? "从未登录" : a.LastLogin);
                it.SubItems.Add(a.Created);
                lv.Items.Add(it);
            }
        }

        private void DoAdd()
        {
            using (InputForm f = new InputForm("新增用户", new string[] { "用户名", "密码", "确认密码" }, new bool[] { false, true, true }))
            {
                if (f.ShowDialog(this) != DialogResult.OK) return;
                string u = f.Values[0].Trim();
                string p1 = f.Values[1];
                string p2 = f.Values[2];
                if (u.Length == 0) { Msg("请输入用户名"); return; }
                if (p1.Length < 6) { Msg("密码长度至少 6 位"); return; }
                if (p1 != p2) { Msg("两次输入的密码不一致"); return; }

                // PBKDF2 + 写库放到后台
                ThreadPool.QueueUserWorkItem(delegate
                {
                    string e = DataStore.CreateAccount(u, p1, "user");
                    if (e == null) Logger.Op("新增用户：" + u);
                    if (this.IsDisposed || this.Disposing) return;
                    try { this.BeginInvoke(new MethodInvoker(delegate { if (e != null) Msg(e); else RefreshList(); })); }
                    catch { }
                });
            }
        }

        private void DoDelete()
        {
            if (lv.SelectedItems.Count == 0) { Msg("请先选择要删除的用户"); return; }
            string u = lv.SelectedItems[0].Text;
            if (MessageBox.Show(this, "确定删除用户「" + u + "」？", "确认", MessageBoxButtons.YesNo, MessageBoxIcon.Warning) != DialogResult.Yes) return;

            // 写库放到后台
            ThreadPool.QueueUserWorkItem(delegate
            {
                string e = DataStore.DeleteAccount(u);
                if (e == null) Logger.Op("删除用户：" + u + "（操作者 " + Session.User + "）");
                if (this.IsDisposed || this.Disposing) return;
                try { this.BeginInvoke(new MethodInvoker(delegate { if (e != null) Msg(e); else RefreshList(); })); }
                catch { }
            });
        }

        private void DoReset()
        {
            if (lv.SelectedItems.Count == 0) { Msg("请先选择用户"); return; }
            string u = lv.SelectedItems[0].Text;
            using (InputForm f = new InputForm("重置密码 - " + u, new string[] { "新密码", "确认新密码" }, new bool[] { true, true }))
            {
                if (f.ShowDialog(this) != DialogResult.OK) return;
                string p1 = f.Values[0];
                string p2 = f.Values[1];
                if (p1.Length < 6) { Msg("密码长度至少 6 位"); return; }
                if (p1 != p2) { Msg("两次输入的密码不一致"); return; }

                // PBKDF2 + 写库放到后台
                ThreadPool.QueueUserWorkItem(delegate
                {
                    string e = DataStore.ResetPassword(u, p1);
                    if (e == null) Logger.Op("重置密码：" + u + "（操作者 " + Session.User + "）");
                    if (this.IsDisposed || this.Disposing) return;
                    try { this.BeginInvoke(new MethodInvoker(delegate { if (e != null) Msg(e); else Msg("密码已重置", MessageBoxIcon.Information); })); }
                    catch { }
                });
            }
        }

        private void Msg(string s, MessageBoxIcon ic = MessageBoxIcon.Error)
        {
            MessageBox.Show(this, s, "用户管理", MessageBoxButtons.OK, ic);
        }
    }
}