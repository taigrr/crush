Read a file by path with line numbers; supports offset and line limit (default {{ .DefaultReadLimit }}, max {{ .MaxViewSizeKB }}KB returned file content section); renders images (PNG, JPEG, GIF, WebP); use ls for directories.

IMPORTANT: When you need to read 2 or more files whose paths you already know, use `multi_view` instead — it reads them all in a single call and is faster and cheaper.
