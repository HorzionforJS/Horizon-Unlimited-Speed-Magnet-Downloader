using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Threading;
using System.Windows.Forms;

namespace MagDownloader
{
    // ============ 登录 / 注册窗口（密码 + 图形验证码管控） ============
    internal class LoginForm : Form
    {
        private const int HEADER_H = 100;

        private TextBox txtUser, txtPass, txtPass2, txtCode;
        private PictureBox picCaptcha;
        private Label lblConfirm, lblCode, lblError;
        private LinkLabel lnkRefresh, lnkToggle;
        private CheckBox chkRemember;
        // 提交时暂存，供 FormClosing 在「认证成功」后写入密码记忆。
        private string lastUser = "", lastPass = "";
        private RButton btnOk;
        private Panel head;
        private bool registerMode;
        // 今天是否已通过一次验证码校验（登录成功即记账），记账后当天免验证码。
        private bool passingDay = false;
        private bool closeHover;
        private Bitmap banner;
        private string headTitle = "登录";

        public string LoggedUser { get { return Session.User; } }

        public LoginForm()
        {
            this.Text = "登录 · 地平线磁力下载";
            this.FormBorderStyle = FormBorderStyle.None;
            this.StartPosition = FormStartPosition.CenterScreen;
            this.ClientSize = new Size(420, 460);
            this.BackColor = Pal.Bg;
            this.Font = new Font("Microsoft YaHei UI", 9F);
            this.DoubleBuffered = true;
            this.KeyPreview = true;

            banner = Assets.LoadBanner();
            BuildUi();
            this.KeyDown += delegate(object s, KeyEventArgs e)
            {
                if (e.KeyCode == Keys.Enter) { e.Handled = true; DoSubmit(); }
                else if (e.KeyCode == Keys.Escape) { e.Handled = true; this.DialogResult = DialogResult.Cancel; this.Close(); }
            };

            // 只有认证成功（DialogResult=OK）才动密码记忆；失败或取消都不碰已有记忆，
            // 否则一次输错密码就把用户记住的账号清掉了。
            this.FormClosing += delegate
            {
                if (this.DialogResult != DialogResult.OK || lastUser.Length == 0) return;
                if (chkRemember.Checked) LoginMemory.Save(lastUser, lastPass);
                else LoginMemory.Forget();
                // 注册成功很少见，不消耗当天的免验证码额度。
                if (!registerMode) CaptchaPass.MarkDone();
            };

            if (DataStore.IsEmpty())
            {
                SetMode(true);
                lblError.Text = "首次使用：请注册一个管理员账号";
                lblError.ForeColor = Pal.Accent;
            }
            else SetMode(false);
            LoadRemembered();
            UpdateCaptchaRow();
        }

        private void BuildUi()
        {
            head = new Panel();
            head.Dock = DockStyle.Top;
            head.Height = HEADER_H;
            head.Paint += delegate(object s, PaintEventArgs e)
            {
                Graphics g = e.Graphics;
                g.SmoothingMode = SmoothingMode.AntiAlias;
                if (banner != null) g.DrawImage(banner, head.ClientRectangle);
                else using (SolidBrush b = new SolidBrush(Pal.Panel)) g.FillRectangle(b, head.ClientRectangle);

                using (Font f1 = new Font("Microsoft YaHei UI", 15F, FontStyle.Bold))
                    TextRenderer.DrawText(g, headTitle, f1, new Rectangle(26, 26, 300, 34), Color.White,
                        TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
                using (Font f2 = new Font("Microsoft YaHei UI", 8.5F))
                    TextRenderer.DrawText(g, "地平线磁力下载 · 密码与验证码管控", f2, new Rectangle(26, 62, 320, 20), Color.FromArgb(225, 255, 255, 255),
                        TextFormatFlags.Left | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);

                // 关闭按钮
                int cx = head.Width - 30, cy = 22;
                if (closeHover)
                    using (SolidBrush b = new SolidBrush(Pal.Danger)) g.FillEllipse(b, cx - 14, cy - 14, 28, 28);
                using (Pen p = new Pen(Color.White, 1.6f))
                {
                    g.DrawLine(p, cx - 5, cy - 5, cx + 5, cy + 5);
                    g.DrawLine(p, cx + 5, cy - 5, cx - 5, cy + 5);
                }
                using (Pen pn = new Pen(Color.FromArgb(255, 14, 24, 46), 1f))
                    g.DrawLine(pn, 0, head.Height - 1, head.Width, head.Height - 1);
            };
            head.MouseDown += new MouseEventHandler(head_MouseDown);
            head.MouseMove += delegate(object s, MouseEventArgs e)
            {
                bool h = e.X >= head.Width - 44 && e.Y <= 44;
                if (h != closeHover) { closeHover = h; head.Invalidate(); }
            };
            head.MouseLeave += delegate(object s, EventArgs e) { if (closeHover) { closeHover = false; head.Invalidate(); } };
            this.Controls.Add(head);

            // 用户名
            this.Controls.Add(MkLabel("用户名", 50, 122));
            txtUser = MkBox(50, 142, 320);
            this.Controls.Add(txtUser);

            // 密码
            this.Controls.Add(MkLabel("密码", 50, 184));
            txtPass = MkBox(50, 204, 320);
            txtPass.UseSystemPasswordChar = true;
            this.Controls.Add(txtPass);

            // 确认密码（注册时显示）
            lblConfirm = MkLabel("确认密码", 50, 252);
            this.Controls.Add(lblConfirm);
            txtPass2 = MkBox(50, 272, 320);
            txtPass2.UseSystemPasswordChar = true;
            this.Controls.Add(txtPass2);

            // 验证码
            lblCode = MkLabel("验证码（不分大小写，点击图片可换）", 50, 318);
            this.Controls.Add(lblCode);

            picCaptcha = new PictureBox();
            picCaptcha.Location = new Point(50, 340);
            picCaptcha.Size = new Size(150, 44);
            picCaptcha.BorderStyle = BorderStyle.FixedSingle;
            picCaptcha.Cursor = Cursors.Hand;
            picCaptcha.SizeMode = PictureBoxSizeMode.Normal;
            picCaptcha.Click += delegate { RefreshCaptcha(); };
            this.Controls.Add(picCaptcha);

            txtCode = MkBox(212, 346, 104);
            txtCode.MaxLength = 6;
            this.Controls.Add(txtCode);

            lnkRefresh = new LinkLabel();
            lnkRefresh.Text = "换一张";
            lnkRefresh.Font = new Font("Microsoft YaHei UI", 8.5F);
            lnkRefresh.Location = new Point(326, 352);
            lnkRefresh.AutoSize = true;
            lnkRefresh.LinkColor = Pal.Accent;
            lnkRefresh.Click += delegate { RefreshCaptcha(); };
            this.Controls.Add(lnkRefresh);

            chkRemember = new CheckBox();
            chkRemember.Text = "记住密码";
            chkRemember.ForeColor = Pal.Muted;
            chkRemember.BackColor = Pal.Bg;
            chkRemember.Font = new Font("Microsoft YaHei UI", 8.5F);
            // 用系统标准样式：Flat 样式在深色背景下会画成一个实心方块，
            // 看不出勾没勾。标准样式是白底方框，对比强也不歧义。
            chkRemember.FlatStyle = FlatStyle.Standard;
            chkRemember.UseVisualStyleBackColor = true;
            chkRemember.AutoSize = true;
            chkRemember.Cursor = Cursors.Hand;
            this.Controls.Add(chkRemember);

            lblError = new Label();
            lblError.Location = new Point(50, 396);
            lblError.Size = new Size(320, 18);
            lblError.ForeColor = Pal.Danger;
            lblError.Font = new Font("Microsoft YaHei UI", 8.5F);
            this.Controls.Add(lblError);

            btnOk = new RButton("登 录", Pal.Accent, Pal.AccentHi, Pal.AccentLo, Color.White);
            btnOk.Location = new Point(50, 420);
            btnOk.Size = new Size(320, 40);
            btnOk.Radius = 12;
            btnOk.Click += delegate { DoSubmit(); };
            this.Controls.Add(btnOk);

            lnkToggle = new LinkLabel();
            lnkToggle.Text = "没有账号？注册新账号";
            lnkToggle.Font = new Font("Microsoft YaHei UI", 8.5F);
            lnkToggle.Location = new Point(50, 472);
            lnkToggle.AutoSize = true;
            lnkToggle.LinkColor = Pal.Muted;
            lnkToggle.Click += delegate { SetMode(!registerMode); };
            this.Controls.Add(lnkToggle);

            txtUser.Focus();
        }

        private Label MkLabel(string txt, int x, int y)
        {
            Label l = new Label();
            l.Text = txt;
            l.ForeColor = Pal.Muted;
            l.Font = new Font("Microsoft YaHei UI", 8.5F);
            l.Location = new Point(x, y);
            l.AutoSize = true;
            return l;
        }

        private TextBox MkBox(int x, int y, int w)
        {
            TextBox t = new TextBox();
            t.Location = new Point(x, y);
            t.Size = new Size(w, 30);
            t.Font = new Font("Microsoft YaHei UI", 10F);
            t.BorderStyle = BorderStyle.FixedSingle;
            t.BackColor = Color.FromArgb(26, 32, 56);
            t.ForeColor = Pal.Text;
            return t;
        }

        private void head_MouseDown(object sender, MouseEventArgs e)
        {
            if (e.Button != MouseButtons.Left) return;
            if (e.X >= head.Width - 44 && e.Y <= 44)
            {
                this.DialogResult = DialogResult.Cancel;
                this.Close();
                return;
            }
            Native.ReleaseCapture();
            Native.SendMessage(this.Handle, 0x00A1, (IntPtr)2, IntPtr.Zero);
        }

        private void SetMode(bool register)
        {
            registerMode = register;
            headTitle = register ? "注册" : "登录";
            this.ClientSize = new Size(420, register ? 524 : 458);
            btnOk.Text = register ? "注 册" : "登 录";
            lnkToggle.Text = register ? "已有账号？返回登录" : "没有账号？注册新账号";

            lblConfirm.Visible = register;
            txtPass2.Visible = register;

            // 验证码及以下控件使用绝对定位（登录布局 / 注册布局）
            int top = register ? 318 : 252;
            lblCode.Location = new Point(50, top);
            picCaptcha.Location = new Point(50, top + 22);
            txtCode.Location = new Point(212, top + 28);
            lnkRefresh.Location = new Point(326, top + 34);
            chkRemember.Location = new Point(50, top + 74);
            lblError.Location = new Point(50, top + 96);
            btnOk.Location = new Point(50, top + 122);
            lnkToggle.Location = new Point(50, top + 174);

            UpdateCaptchaRow();
            ClearError();
            head.Invalidate();
        }

        // 载入上次「记住密码」的账号；没有记忆就什么都不做。
        private void LoadRemembered()
        {
            string u = LoginMemory.SavedUser;
            if (u.Length == 0) return;

            txtUser.Text = u;
            chkRemember.Checked = true;

            string p = LoginMemory.LoadPassword();
            if (p.Length > 0)
            {
                // 密码已自动填好，光标直接落到验证码，少按一次 Tab。
                txtPass.Text = p;
                if (passingDay) btnOk.Focus();
                else txtCode.Focus();
            }
            else
            {
                txtPass.Focus();
            }
        }

        private void RefreshCaptcha()
        {
            if (passingDay) return;
            Captcha.NewCode(4);
            if (picCaptcha.Image != null) picCaptcha.Image.Dispose();
            picCaptcha.Image = Captcha.Render(Captcha.Code, 150, 44);
            txtCode.Clear();
        }

        // UpdateCaptchaRow 按「今天是否已通过验证」决定这一行的形态。
        //
        // 免验证码时把控件藏起来并换一行说明，而不是留着图让人以为还要输：
        // 用户看到空框会去纠结要不要填，看到这行字就知道今天已经过了。
        private void UpdateCaptchaRow()
        {
            passingDay = CaptchaPass.DoneToday();

            if (registerMode)
            {
                // 注册必须每次都过验证码，不给免。
                lblCode.Text = "验证码（不分大小写，点击图片可换）";
                lblCode.ForeColor = Pal.Muted;
                picCaptcha.Visible = true;
                txtCode.Visible = true;
                lnkRefresh.Visible = true;
                if (picCaptcha.Image == null) RefreshCaptcha();
            }
            else if (passingDay)
            {
                lblCode.Text = "今日已通过验证，无需输入验证码";
                lblCode.ForeColor = Pal.Success;
                picCaptcha.Visible = false;
                txtCode.Visible = false;
                lnkRefresh.Visible = false;
                // 藏起来的图要清掉，否则下次输入错密码走 RefreshCaptcha 时会
                // 换出一张新的，而这一行明明是隐藏状态。
                if (picCaptcha.Image != null) { picCaptcha.Image.Dispose(); picCaptcha.Image = null; }
                txtCode.Clear();
            }
            else
            {
                lblCode.Text = "验证码（不分大小写，点击图片可换）";
                lblCode.ForeColor = Pal.Muted;
                picCaptcha.Visible = true;
                txtCode.Visible = true;
                lnkRefresh.Visible = true;
                RefreshCaptcha();
            }
        }

        private void ShowError(string msg) { lblError.Text = msg; lblError.ForeColor = Pal.Danger; }
        private void ClearError() { lblError.Text = ""; }

        private void DoSubmit()
        {
            if (!btnOk.Enabled) return;   // 防止重复提交
            ClearError();
            string user = txtUser.Text.Trim();
            string pass = txtPass.Text;

            // 今天已经过验证码且是登录（非注册）时不再校验。
            bool needCaptcha = registerMode || !passingDay;
            if (needCaptcha &&
                (txtCode.Text.Trim().Length == 0 ||
                 !string.Equals(txtCode.Text.Trim(), Captcha.Code, StringComparison.OrdinalIgnoreCase)))
            {
                ShowError("验证码错误，请重新输入");
                RefreshCaptcha();
                txtCode.Focus();
                Logger.Auth("登录/注册验证码错误：" + (user.Length == 0 ? "(空)" : user));
                return;
            }

            if (user.Length == 0) { ShowError("请输入用户名"); return; }
            if (pass.Length < 6) { ShowError("密码长度至少 6 位"); return; }
            if (registerMode)
            {
                if (txtPass2.Text != pass) { ShowError("两次输入的密码不一致"); return; }
            }

            // 云端/本地认证放到后台线程，避免服务器慢或不可达时界面卡死
            btnOk.Enabled = false;
            lblError.ForeColor = Pal.Muted;
            lblError.Text = "正在连接服务器，请稍候…";

            lastUser = user;
            lastPass = pass;

            bool reg = registerMode;
            ThreadPool.QueueUserWorkItem(delegate { SubmitAuth(user, pass, reg); });
        }

        // 后台线程：执行「云端优先、不可达回退本地」的完整认证流程
        private void SubmitAuth(string user, string pass, bool reg)
        {
            if (reg)
            {
                CloudConfig.Load();
                CloudResult cr = CloudAuth.Register(user, pass);
                if (cr.Ok)
                {
                    Session.User = user;
                    CloudSession.Adopt(cr);
                    Logger.Auth("云端注册成功：" + user + (cr.IsAdmin ? "（管理员）" : ""));
                    Ui(delegate { this.DialogResult = DialogResult.OK; this.Close(); });
                    return;
                }
                if (!cr.NetworkError)
                {
                    Ui(delegate { Failure(cr.Error); });
                    return;
                }

                // 网络不可达 → 回退本地注册
                bool first = DataStore.IsEmpty();
                string role = first ? "admin" : "user";
                string err = DataStore.CreateAccount(user, pass, role);
                if (err != null) { Ui(delegate { Failure(err); }); return; }
                Session.User = user; Session.IsAdmin = role == "admin";
                Logger.Auth("本地注册成功：" + user + "（" + role + "）");
                Ui(delegate { this.DialogResult = DialogResult.OK; this.Close(); });
            }
            else
            {
                CloudConfig.Load();
                CloudResult cr = CloudAuth.Login(user, pass);
                if (cr.Ok)
                {
                    Session.User = user;
                    CloudSession.Adopt(cr);
                    Logger.Auth("云端登录成功：" + user + (cr.IsAdmin ? "（管理员）" : "")
                        + (cr.RefreshToken.Length > 0 ? "（已获得续期令牌）" : ""));
                    Ui(delegate { this.DialogResult = DialogResult.OK; this.Close(); });
                    return;
                }
                if (!cr.NetworkError)
                {
                    // 服务器可达但认证失败（用户名/密码错）→ 以云端为准，不回退本地
                    Logger.Auth("云端登录失败：" + user + " -> " + cr.Error);
                    Ui(delegate { Failure(cr.Error); });
                    return;
                }

                // 网络不可达 → 回退本地登录
                string msg;
                LoginResult r = DataStore.Authenticate(user, pass, out msg);
                if (r == LoginResult.Ok)
                {
                    Session.User = user; Session.IsAdmin = DataStore.IsAdmin(user);
                    Logger.Auth("本地登录成功：" + user + (Session.IsAdmin ? "（管理员）" : ""));
                    Ui(delegate { this.DialogResult = DialogResult.OK; this.Close(); });
                }
                else
                {
                    Logger.Auth("本地登录失败：" + user + " -> " + msg);
                    Ui(delegate { Failure(msg); });
                }
            }
        }

        // UI 线程：认证失败时的统一提示与状态复位
        private void Failure(string msg)
        {
            ShowError(msg);
            txtPass.Clear();

            if (passingDay)
            {
                // 今天免验证码：别因为一次密码输错就把刚填好的验证码换掉——
                // 用户此时要改的是密码，不是验证码。
                txtPass.Focus();
                return;
            }

            RefreshCaptcha();
            txtCode.Focus();
        }

        // 后台线程安全地调度回 UI 线程（窗口已销毁则忽略）
        private void Ui(Action a)
        {
            if (this.IsDisposed || this.Disposing) return;
            if (this.InvokeRequired) { try { this.BeginInvoke(new MethodInvoker(delegate { a(); })); } catch { } }
            else a();
        }
    }
}