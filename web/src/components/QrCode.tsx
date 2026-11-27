import { useEffect, useState } from "react";
import QRCode from "qrcode";

interface QrCodeProps {
  value: string;
  size?: number;
  label?: string;
}

export function QrCode({ value, size = 184, label = "TOTP 二维码" }: QrCodeProps) {
  const [dataUrl, setDataUrl] = useState<string>("");
  const [error, setError] = useState<string>("");

  useEffect(() => {
    let cancelled = false;
    if (!value) {
      setDataUrl("");
      return;
    }
    QRCode.toDataURL(value, {
      width: size * 2,
      margin: 4,
      color: {
        dark: "#0c0f14",
        light: "#ffffff",
      },
      errorCorrectionLevel: "M",
    })
      .then((url) => {
        if (!cancelled) {
          setError("");
          setDataUrl(url);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "二维码生成失败");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [value, size]);

  if (error) {
    return (
      <div
        style={{ width: size, height: size }}
        className="flex items-center justify-center rounded-md border border-danger/40 bg-recessed p-3 text-center text-xs text-danger"
      >
        {error}
      </div>
    );
  }

  if (!dataUrl) {
    return (
      <div
        style={{ width: size, height: size }}
        className="flex items-center justify-center rounded-md border border-hairline bg-recessed text-xs text-ink-muted"
      >
        生成中...
      </div>
    );
  }

  return (
    <div className="inline-block rounded-md border border-hairline bg-white p-2">
      <img
        src={dataUrl}
        alt={label}
        width={size}
        height={size}
        className="block rounded-sm"
      />
    </div>
  );
}
