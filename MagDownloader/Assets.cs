using System;
using System.Drawing;
using System.IO;
using System.Reflection;

namespace MagDownloader
{
    // ============ 共享资源（地平线横幅） ============
    internal static class Assets
    {
        public static Bitmap LoadBanner()
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
                        using (Image tmp = Image.FromStream(ms)) return new Bitmap(tmp);
                    }
                }
            }
            catch { return null; }
        }
    }
}