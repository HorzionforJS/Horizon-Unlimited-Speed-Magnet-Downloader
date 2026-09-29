using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 应用路径（数据目录 / 日志目录，支持测试时重定向） ============
    internal static class AppPaths
    {
        private static string _override = null;

        public static void OverrideRoot(string root) { _override = root; }

        public static string Root
        {
            get
            {
                if (_override != null) return _override;
                return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "MagDownloader");
            }
        }

        public static string Data { get { return Path.Combine(Root, "data"); } }
        public static string Logs { get { return Path.Combine(Root, "logs"); } }
        public static string UsersFile { get { return Path.Combine(Data, "users.db"); } }

        public static void Ensure()
        {
            Directory.CreateDirectory(Data);
            Directory.CreateDirectory(Logs);
        }
    }

    // ============ 客户端版本 ============
    //
    // 改版本号时只要改这里一处：标题栏、关于、更新检查都读它，
    // 不会出现「界面上写着 v1.2、实际已经是 v1.3」这种对不上号的情况。
    internal static class AppVersion
    {
        public const string Number = "1.3.0";

        public static string Display { get { return "v" + Number; } }
    }

    // ============ 当前登录会话 ============
    internal static class Session
    {
        public static string User = "";
        public static bool IsAdmin = false;
        public static bool LogoutRequested = false;
        public static string Token = "";         // 云端 access JWT（有效期 12 小时，过期后自动续期）
        public static string RefreshToken = "";  // 云端 refresh token（有效期 30 天，用于静默续期，避免用户反复登录）
    }

    // ============ 文件数据日志存储（app / login / op 三类日志，自动轮转） ============
    internal static class Logger
    {
        private static readonly object Gate = new object();

        public static void App(string msg) { Write("app.log", msg); }
        public static void Auth(string msg) { Write("login.log", msg); }
        public static void Op(string msg) { Write("op.log", msg); }

        private static void Write(string file, string msg)
        {
            try
            {
                lock (Gate)
                {
                    AppPaths.Ensure();
                    string path = Path.Combine(AppPaths.Logs, file);
                    try
                    {
                        FileInfo fi = new FileInfo(path);
                        if (fi.Exists && fi.Length > 2 * 1024 * 1024)
                        {
                            string bak = Path.Combine(AppPaths.Logs, file + "." + DateTime.Now.ToString("yyyyMMddHHmmss") + ".old");
                            File.Move(path, bak);
                        }
                    }
                    catch { }
                    string line = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff") + " | " + Session.User + " | " + msg;
                    File.AppendAllText(path, line + Environment.NewLine, Encoding.UTF8);
                }
            }
            catch { }
        }
    }

    // ============ 账号记录（users.db 数据库中的一行） ============
    internal class UserAccount
    {
        public string User { get; set; }
        public string Role { get; set; }
        public string Salt { get; set; }
        public string Hash { get; set; }
        public int Iter { get; set; }
        public string Created { get; set; }
        public string LastLogin { get; set; }
        public int Failed { get; set; }
        public string LockUntil { get; set; }
    }

    // ============ 密码散列（PBKDF2 加盐，恒定时间比较，绝不明文存储） ============
    internal static class PasswordHasher
    {
        public const int Iterations = 100000;
        public const int SaltSize = 16;
        public const int HashSize = 32;

        public static byte[] NewSalt()
        {
            byte[] b = new byte[SaltSize];
            using (RNGCryptoServiceProvider rng = new RNGCryptoServiceProvider()) rng.GetBytes(b);
            return b;
        }

        public static byte[] Hash(string password, byte[] salt, int iter)
        {
            using (Rfc2898DeriveBytes kdf = new Rfc2898DeriveBytes(password, salt, iter)) return kdf.GetBytes(HashSize);
        }

        public static bool Verify(string password, UserAccount a)
        {
            byte[] salt = Convert.FromBase64String(a.Salt);
            byte[] expect = Convert.FromBase64String(a.Hash);
            byte[] actual = Hash(password, salt, a.Iter);
            if (expect.Length != actual.Length) return false;
            int diff = 0;
            for (int i = 0; i < expect.Length; i++) diff |= expect[i] ^ actual[i];
            return diff == 0;
        }

        public static string Now() { return DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss"); }
    }

    public enum LoginResult { Ok, NoUser, BadPassword, Locked }

    // ============ 账号数据库（users.db）与密码/验证码管控 ============
    internal static class DataStore
    {
        public const int MaxFailed = 5;
        public const int LockMinutes = 5;

        private static List<UserAccount> accounts;
        private static readonly object Gate = new object();
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        public static void Init()
        {
            AppPaths.Ensure();
            lock (Gate) { accounts = Load(); }
        }

        private static List<UserAccount> Load()
        {
            try
            {
                if (File.Exists(AppPaths.UsersFile))
                {
                    string s = File.ReadAllText(AppPaths.UsersFile, Encoding.UTF8);
                    if (!string.IsNullOrWhiteSpace(s))
                        return Json.Deserialize<List<UserAccount>>(s) ?? new List<UserAccount>();
                }
            }
            catch (Exception ex) { Logger.App("读取账号数据库失败：" + ex.Message); }
            return new List<UserAccount>();
        }

        public static void Save()
        {
            try { File.WriteAllText(AppPaths.UsersFile, Json.Serialize(accounts), Encoding.UTF8); }
            catch (Exception ex) { Logger.App("写入账号数据库失败：" + ex.Message); }
        }

        public static bool IsEmpty() { lock (Gate) return accounts.Count == 0; }

        public static UserAccount Find(string user)
        {
            lock (Gate)
            {
                foreach (UserAccount a in accounts)
                    if (string.Equals(a.User, user, StringComparison.OrdinalIgnoreCase)) return a;
                return null;
            }
        }

        public static bool IsAdmin(string user)
        {
            UserAccount a = Find(user);
            return a != null && a.Role == "admin";
        }

        public static bool VerifyPassword(string user, string password)
        {
            UserAccount a = Find(user);
            if (a == null) return false;
            return PasswordHasher.Verify(password, a);
        }

        public static string CreateAccount(string user, string password, string role)
        {
            lock (Gate)
            {
                if (Find(user) != null) return "该用户名已存在";
                UserAccount a = new UserAccount();
                a.User = user;
                a.Role = role;
                a.Salt = Convert.ToBase64String(PasswordHasher.NewSalt());
                a.Iter = PasswordHasher.Iterations;
                a.Hash = Convert.ToBase64String(PasswordHasher.Hash(password, Convert.FromBase64String(a.Salt), a.Iter));
                a.Created = PasswordHasher.Now();
                a.LastLogin = "";
                a.Failed = 0;
                a.LockUntil = "";
                accounts.Add(a);
                Save();
                return null;
            }
        }

        public static string ResetPassword(string user, string newPassword)
        {
            lock (Gate)
            {
                UserAccount a = Find(user);
                if (a == null) return "用户不存在";
                a.Salt = Convert.ToBase64String(PasswordHasher.NewSalt());
                a.Hash = Convert.ToBase64String(PasswordHasher.Hash(newPassword, Convert.FromBase64String(a.Salt), a.Iter));
                a.LockUntil = "";
                a.Failed = 0;
                Save();
                return null;
            }
        }

        public static string DeleteAccount(string user)
        {
            lock (Gate)
            {
                UserAccount a = Find(user);
                if (a == null) return "用户不存在";
                if (string.Equals(a.User, Session.User, StringComparison.OrdinalIgnoreCase)) return "不能删除当前登录账号";
                accounts.Remove(a);
                Save();
                return null;
            }
        }

        public static List<UserAccount> All()
        {
            lock (Gate)
            {
                List<UserAccount> copy = new List<UserAccount>();
                foreach (UserAccount a in accounts) copy.Add(a);
                return copy;
            }
        }

        public static LoginResult Authenticate(string user, string password, out string message)
        {
            message = "";
            UserAccount a = Find(user);
            if (a == null) { message = "用户名不存在"; return LoginResult.NoUser; }

            if (!string.IsNullOrEmpty(a.LockUntil))
            {
                DateTime lu;
                if (DateTime.TryParse(a.LockUntil, CultureInfo.InvariantCulture, DateTimeStyles.None, out lu) && DateTime.Now < lu)
                {
                    int left = (int)Math.Ceiling((lu - DateTime.Now).TotalMinutes);
                    message = "账号已锁定，请 " + Math.Max(1, left) + " 分钟后再试";
                    return LoginResult.Locked;
                }
                a.LockUntil = "";
            }

            if (!PasswordHasher.Verify(password, a))
            {
                a.Failed++;
                if (a.Failed >= MaxFailed)
                {
                    a.LockUntil = DateTime.Now.AddMinutes(LockMinutes).ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture);
                    a.Failed = 0;
                    message = "连续错误次数过多，账号已锁定 " + LockMinutes + " 分钟";
                }
                else
                {
                    message = "密码错误（还可尝试 " + (MaxFailed - a.Failed) + " 次）";
                }
                Save();
                return LoginResult.BadPassword;
            }

            a.Failed = 0;
            a.LockUntil = "";
            a.LastLogin = PasswordHasher.Now();
            Save();
            return LoginResult.Ok;
        }
    }
}