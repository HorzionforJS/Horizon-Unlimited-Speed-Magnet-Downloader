using System;
using System.Drawing;
using System.Drawing.Drawing2D;

namespace MagDownloader
{
    // ============ 图形验证码（GDI+ 生成，噪声 + 旋转 + 干扰线） ============
    internal static class Captcha
    {
        private const string Alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ";
        public static string Code = "";

        public static string NewCode(int length)
        {
            string c = "";
            Random r = new Random(unchecked((int)DateTime.Now.Ticks) ^ Environment.TickCount);
            for (int i = 0; i < length; i++) c += Alphabet[r.Next(Alphabet.Length)];
            Code = c;
            return c;
        }

        public static Bitmap Render(string code, int width, int height)
        {
            Bitmap bmp = new Bitmap(width, height);
            using (Graphics g = Graphics.FromImage(bmp))
            {
                g.SmoothingMode = SmoothingMode.AntiAlias;
                Random r = new Random(unchecked((int)DateTime.Now.Ticks) ^ (code != null ? code.GetHashCode() : 0));

                using (LinearGradientBrush b = new LinearGradientBrush(new Rectangle(0, 0, width, height),
                    Color.FromArgb(26, 32, 56), Color.FromArgb(14, 18, 31), LinearGradientMode.Vertical))
                    g.FillRectangle(b, 0, 0, width, height);

                for (int i = 0; i < 4; i++)
                    using (Pen p = new Pen(Color.FromArgb(r.Next(90, 160), r.Next(150, 220), r.Next(150, 220), r.Next(150, 220)), 1f))
                        g.DrawLine(p, r.Next(width), r.Next(height), r.Next(width), r.Next(height));

                for (int i = 0; i < 70; i++)
                    using (SolidBrush b = new SolidBrush(Color.FromArgb(r.Next(80, 150), r.Next(150, 220), r.Next(150, 220), r.Next(150, 220))))
                        g.FillEllipse(b, r.Next(width), r.Next(height), 2, 2);

                int n = code.Length;
                float cw = (float)width / n;
                for (int i = 0; i < n; i++)
                {
                    float ang = r.Next(-24, 24);
                    using (Font f = new Font("Arial", r.Next(18, 22), FontStyle.Bold))
                    {
                        Color cc = Color.FromArgb(255, r.Next(210, 255), r.Next(210, 255), r.Next(220, 255));
                        DrawCentered(g, code[i].ToString(), f, cc, cw * i + cw / 2, height / 2 + 2, ang);
                    }
                }
            }
            return bmp;
        }

        private static void DrawCentered(Graphics g, string s, Font f, Color c, float cx, float cy, float angle)
        {
            SizeF sz = g.MeasureString(s, f);
            GraphicsState st = g.Save();
            g.TranslateTransform(cx, cy);
            g.RotateTransform(angle);
            using (SolidBrush b = new SolidBrush(c)) g.DrawString(s, f, b, -sz.Width / 2, -sz.Height / 2);
            g.Restore(st);
        }
    }
}