using System;
using System.Drawing;
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