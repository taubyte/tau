#!/bin/bash

set -x

export GOPROXY=https://proxy.golang.org,direct
export GOSUMDB=sum.golang.org
export GOTOOLCHAIN=auto

mkdir /afero
git clone --branch v1.9.5 --single-branch https://github.com/spf13/afero.git /afero
cd /afero
git apply - << EOF
diff -ruN afero-main/const_bsds.go afero/const_bsds.go
--- afero-main/const_bsds.go	2024-06-06 14:59:25.758882489 -0500
+++ afero/const_bsds.go	2024-06-06 14:13:54.918853261 -0500
@@ -11,8 +11,8 @@
 // See the License for the specific language governing permissions and
 // limitations under the License.
 
-//go:build aix || darwin || openbsd || freebsd || netbsd || dragonfly
-// +build aix darwin openbsd freebsd netbsd dragonfly
+//go:build aix || darwin || openbsd || freebsd || netbsd || dragonfly || zos || wasip1 || wasi || wasm
+// +build aix darwin openbsd freebsd netbsd dragonfly zos wasip1 wasi wasm
 
 package afero
 
diff -ruN afero-main/const_win_unix.go afero/const_win_unix.go
--- afero-main/const_win_unix.go	2024-06-06 14:59:25.758882489 -0500
+++ afero/const_win_unix.go	2024-06-06 14:15:20.907229503 -0500
@@ -10,8 +10,8 @@
 // WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 // See the License for the specific language governing permissions and
 // limitations under the License.
-//go:build !darwin && !openbsd && !freebsd && !dragonfly && !netbsd && !aix
-// +build !darwin,!openbsd,!freebsd,!dragonfly,!netbsd,!aix
+//go:build !darwin && !openbsd && !freebsd && !dragonfly && !netbsd && !aix && !zos && !wasip1 && !wasi && !wasm
+// +build !darwin,!openbsd,!freebsd,!dragonfly,!netbsd,!aix,!zos,!wasip1,!wasi,!wasm
 
 package afero
 
diff -ruN afero-main/no_wasi.go afero/no_wasi.go
--- afero-main/no_wasi.go	1969-12-31 18:00:00.000000000 -0600
+++ afero/no_wasi.go	2024-06-06 14:28:32.494686063 -0500
@@ -0,0 +1,12 @@
+//go:build !wasi && !wasm
+// +build !wasi,!wasm
+
+package afero
+
+import (
+	"os"
+)
+
+func chown(name string, uid int, gid int) error {
+	return os.Chown(name, uid, gid)
+}
diff -ruN afero-main/os.go afero/os.go
--- afero-main/os.go	2024-06-06 14:57:48.886447128 -0500
+++ afero/os.go	2024-06-06 14:27:09.478323916 -0500
@@ -92,7 +92,7 @@
 }
 
 func (OsFs) Chown(name string, uid, gid int) error {
-	return os.Chown(name, uid, gid)
+	return chown(name, uid, gid)
 }
 
 func (OsFs) Chtimes(name string, atime time.Time, mtime time.Time) error {
diff -ruN afero-main/sftpfs/file.go afero/sftpfs/file.go
--- afero-main/sftpfs/file.go	2024-06-06 14:59:25.758882489 -0500
+++ afero/sftpfs/file.go	2024-06-06 14:08:55.929542638 -0500
@@ -64,9 +64,8 @@
 	return f.fd.Read(b)
 }
 
-// TODO
 func (f *File) ReadAt(b []byte, off int64) (n int, err error) {
-	return 0, nil
+	return f.fd.ReadAt(b, off)
 }
 
 func (f *File) Readdir(count int) (res []os.FileInfo, err error) {
diff -ruN afero-main/wasi.go afero/wasi.go
--- afero-main/wasi.go	1969-12-31 18:00:00.000000000 -0600
+++ afero/wasi.go	2024-06-06 14:28:05.022566225 -0500
@@ -0,0 +1,8 @@
+//go:build wasi || wasm
+// +build wasi wasm
+
+package afero
+
+func chown(string, int, int) error {
+	return nil
+}
EOF

(
    cd /src
    go version
    go mod tidy
)

. /utils/wasm.sh

build debug "${FILENAME}"
ret=$?
echo -n $ret > /out/ret-code
exit $ret
