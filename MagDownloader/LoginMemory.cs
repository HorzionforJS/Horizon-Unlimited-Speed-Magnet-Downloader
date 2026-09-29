using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 登录密码记忆 ============
    //
    // 用 Windows DPAPI（CurrentUser 作用域）加密后落盘。密文与当前 Windows 账户绑定，
    // 把 data\login.cfg 拷到另一台机器或另一个账户下都解不开——这比自己生成密钥再存在
    // 本机要实在，后者等于把钥匙和保险箱放在同一个抽屉里。
    //
    // 任何一步出错都只表现为「没有记忆」：绝不能让登录界面因此报错。
    internal static class LoginMemory
    {
        private const string EntropyText = "MagDownloader.LoginMemory.v1";
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        public static string FilePath { get { return Path.Combine(AppPaths.Data, "login.cfg"); } }

        // 上次记住的用户名（无记忆时为空串）。
        public static string SavedUser { get { return Read().User; } }

        // 取出记住的密码；无记忆或解密失败时返回空串。
        public static string LoadPassword()
        {
            Record r = Read();
            if (r.Secret.Length == 0) return "";
            try
            {
                byte[] blob = Convert.FromBase64String(r.Secret);
                byte[] raw = ProtectedData.Unprotect(blob, Entropy(), DataProtectionScope.CurrentUser);
                return Encoding.UTF8.GetString(raw);
            }
            catch (Exception ex)
            {
                // 常见于换了机器或换了 Windows 账户后把数据目录一起拷过来：解不开就当作
                // 没记忆，顺手清掉，免得每次启动都白试一遍。
                Logger.App("密码记忆解密失败，已清除：" + ex.Message);
                Forget();
                return "";
            }
        }

        public static void Save(string user, string password)
        {
            if (string.IsNullOrEmpty(user) || string.IsNullOrEmpty(password)) return;
            try
            {
                AppPaths.Ensure();
                byte[] blob = ProtectedData.Protect(Encoding.UTF8.GetBytes(password), Entropy(),
                    DataProtectionScope.CurrentUser);
                Dictionary<string, object> d = new Dictionary<string, object>();
                d["User"] = user;
                d["Secret"] = Convert.ToBase64String(blob);
                d["Saved"] = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                File.WriteAllText(FilePath, Json.Serialize(d), Encoding.UTF8);
            }
            catch (Exception ex) { Logger.App("保存密码记忆失败：" + ex.Message); }
        }

        public static void Forget()
        {
            try { if (File.Exists(FilePath)) File.Delete(FilePath); }
            catch (Exception ex) { Logger.App("清除密码记忆失败：" + ex.Message); }
        }

        private static byte[] Entropy() { return Encoding.UTF8.GetBytes(EntropyText); }

        private struct Record { public string User; public string Secret; public string Saved; }

        private static Record Read()
        {
            Record r = new Record();
            r.User = ""; r.Secret = ""; r.Saved = "";

            string s;
            try
            {
                if (!File.Exists(FilePath)) return r;
                s = File.ReadAllText(FilePath, Encoding.UTF8);
            }
            catch (Exception ex)
            {
                // 这里只会是 IO 层面的偶发失败（比如被占用）。
                // 不动文件：下次读可能就好了。
                Logger.App("读取密码记忆失败：" + ex.Message);
                return r;
            }

            if (string.IsNullOrWhiteSpace(s))
            {
                DropCorrupt("内容为空");
                return r;
            }

            Dictionary<string, object> d;
            try { d = Json.DeserializeObject(s) as Dictionary<string, object>; }
            catch (Exception ex)
            {
                // 能读到文件却解不成 JSON，说明内容已损坏（手改过、写到一半）。
                // 这种文件永远好不了，每次启动白读一遍不如直接清掉。
                DropCorrupt(ex.Message);
                return r;
            }

            if (d == null)
            {
                DropCorrupt("结构不符");
                return r;
            }

            r.User = Str(d, "User");
            r.Secret = Str(d, "Secret");
            r.Saved = Str(d, "Saved");
            return r;
        }

        private static void DropCorrupt(string why)
        {
            Logger.App("密码记忆内容无效，已清除：" + why);
            Forget();
        }

        private static string Str(Dictionary<string, object> d, string k)
        {
            return d != null && d.ContainsKey(k) && d[k] != null ? d[k].ToString() : "";
        }
    }
}
