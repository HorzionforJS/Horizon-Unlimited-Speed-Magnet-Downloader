using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Web.Script.Serialization;

namespace MagDownloader
{
    // ============ 当日免验证码 ============
    //
    // 需求：每天成功登录一次之后，当天再打开就不用再输验证码。
    //
    // 这里存的是「最后一次通过验证码的日期」，不是密码——密码记忆是另一件事
    // （见 LoginMemory）。所以没开「记住密码」也能享受免验证码：登录时手输一次
    // 密码，验证码当天不用再输。
    //
    // 记录用 DPAPI 签名。这不是为了防专业的攻击者——能登进你 Windows 账户的人
    // 本来就能改本机任何东西——而是为了让「把日期手改成明天」这种顺手篡改
    // 变成一个要写代码的活儿。用当前账户就能生成的签名，正好够这个粒度。
    internal static class CaptchaPass
    {
        private const string EntropyText = "MagDownloader.CaptchaPass.v1";
        private static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        public static string FilePath { get { return Path.Combine(AppPaths.Data, "captcha.day"); } }

        // DoneToday 返回今天是否已通过一次验证码校验。
        // 任何异常都按「没有通过」处理：宁可多要一次验证码，不可漏放。
        public static bool DoneToday()
        {
            try
            {
                string day = ReadDay();
                if (day.Length == 0) return false;
                return string.Equals(day, Today(), StringComparison.Ordinal);
            }
            catch (Exception ex)
            {
                Logger.App("读取免验证码状态失败：" + ex.Message);
                return false;
            }
        }

        // MarkDone 记下「今天已通过验证」；在登录成功那一刻调用。
        public static void MarkDone()
        {
            try
            {
                AppPaths.Ensure();
                string day = Today();

                Dictionary<string, object> d = new Dictionary<string, object>();
                d["Day"] = day;
                d["Sig"] = Sign(day);
                byte[] plain = Encoding.UTF8.GetBytes(Json.Serialize(d));

                byte[] blob = ProtectedData.Protect(plain, Entropy(), DataProtectionScope.CurrentUser);
                File.WriteAllText(FilePath, Convert.ToBase64String(blob), Encoding.UTF8);
            }
            catch (Exception ex) { Logger.App("写入免验证码状态失败：" + ex.Message); }
        }

        public static void Clear()
        {
            try { if (File.Exists(FilePath)) File.Delete(FilePath); }
            catch (Exception ex) { Logger.App("清除免验证码状态失败：" + ex.Message); }
        }

        private static string Today() { return DateTime.Now.ToString("yyyy-MM-dd"); }
        private static byte[] Entropy() { return Encoding.UTF8.GetBytes(EntropyText); }

        private static string Sign(string day)
        {
            byte[] mac = new HMACSHA256(Entropy()).ComputeHash(Encoding.UTF8.GetBytes(day));
            return Convert.ToBase64String(mac);
        }

        private static string ReadDay()
        {
            if (!File.Exists(FilePath)) return "";

            byte[] blob;
            try { blob = Convert.FromBase64String(File.ReadAllText(FilePath, Encoding.UTF8).Trim()); }
            catch { Disk(); return ""; }

            byte[] plain;
            try { plain = ProtectedData.Unprotect(blob, Entropy(), DataProtectionScope.CurrentUser); }
            catch (Exception ex)
            {
                // 换了机器或换了 Windows 账户会把数据目录一起带过来，这里必然解不开。
                Logger.App("免验证码记录解密失败，已清除：" + ex.Message);
                Disk();
                return "";
            }

            Dictionary<string, object> d;
            try { d = Json.DeserializeObject(Encoding.UTF8.GetString(plain)) as Dictionary<string, object>; }
            catch { Disk(); return ""; }
            if (d == null) { Disk(); return ""; }

            string day = Str(d, "Day");
            string sig = Str(d, "Sig");
            if (day.Length == 0 || sig.Length == 0) { Disk(); return ""; }

            // 签名对不上说明内容被改过（比如手改日期），当作没登录过。
            if (!string.Equals(sig, Sign(day), StringComparison.Ordinal))
            {
                Logger.App("免验证码记录签名不符（疑似被修改），已清除");
                Disk();
                return "";
            }
            return day;
        }

        private static string Str(Dictionary<string, object> d, string k)
        {
            return d != null && d.ContainsKey(k) && d[k] != null ? d[k].ToString() : "";
        }

        private static void Disk()
        {
            try { if (File.Exists(FilePath)) File.Delete(FilePath); } catch { }
        }
    }
}
