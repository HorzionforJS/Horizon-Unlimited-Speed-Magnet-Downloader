using System;
using System.IO;
using System.Windows.Forms;

namespace MagDownloader
{
    internal static class Program
    {
        [STAThread]
        public static void Main()
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.SetUnhandledExceptionMode(UnhandledExceptionMode.CatchException);
            Application.ThreadException += delegate(object s, System.Threading.ThreadExceptionEventArgs ex)
            {
                LogCrash("[UI] " + ex.Exception);
            };
            AppDomain.CurrentDomain.UnhandledException += delegate(object s, UnhandledExceptionEventArgs ex)
            {
                LogCrash("[DOM] " + ex.ExceptionObject);
            };

            try
            {
                DataStore.Init();
                Logger.App("========== 应用启动 ==========");

                while (true)
                {
                    Session.LogoutRequested = false;

                    using (LoginForm login = new LoginForm())
                    {
                        if (login.ShowDialog() != DialogResult.OK)
                        {
                            Logger.Auth("取消登录，应用退出");
                            break;
                        }
                        Logger.Auth("登录成功：" + Session.User + (Session.IsAdmin ? "（管理员）" : ""));
                    }

                    using (MainForm main = new MainForm())
                    {
                        Application.Run(main);
                    }

                    // 「退出登录」：连同当日免验证码一起作废，强制重新走一次验证码。
                    // 这条路径不能和「关窗口」共用——关窗口是日常操作，退出登录是明确的「我要重新验证」。
                    if (Session.LogoutRequested) CaptchaPass.Clear();

                    if (!Session.LogoutRequested) break;
                }
            }
            catch (Exception ex)
            {
                LogCrash("[MAIN] " + ex);
                try { MessageBox.Show("程序发生错误：" + ex.Message, "地平线磁力下载", MessageBoxButtons.OK, MessageBoxIcon.Error); } catch { }
            }
            Logger.App("应用退出");
        }

        // 崩溃日志走 AppPaths，测试重定向（AppPaths.OverrideRoot）下才会写进测试目录，
        // 不会在跑单测时往真实 %LOCALAPPDATA% 里丢垃圾。
        private static void LogCrash(string msg)
        {
            try
            {
                AppPaths.Ensure();
                string file = Path.Combine(AppPaths.Logs, "error.log");
                RollIfLarge(file);
                File.AppendAllText(file,
                    DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff") + " " + msg + Environment.NewLine,
                    System.Text.Encoding.UTF8);
            }
            catch { }
        }

        // 崩溃日志也要轮转：反复崩溃时 error.log 会无限增长，磁盘塞满后连日志本身都写不进去。
        private static void RollIfLarge(string file)
        {
            try
            {
                FileInfo fi = new FileInfo(file);
                if (!fi.Exists || fi.Length < 1024 * 1024) return;
                string bak = file + "." + DateTime.Now.ToString("yyyyMMddHHmmss") + ".old";
                File.Move(file, bak);
            }
            catch { }
        }
    }
}