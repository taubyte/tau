// secret prints one secret a build or a container was granted, read from the vault the
// platform serves on a unix socket beside it. It is static and needs nothing from the
// image it runs in.
//
//   /.taubyte/secret <label>
//
// Exit codes: 0 the value is on stdout, unchanged; 1 the vault refused (the reason is
// on stderr); 2 usage; 3 the vault could not be reached or did not answer as one.

const std = @import("std");
const posix = std.posix;

const socket_path = "/.taubyte/vault.sock";
const max_label = 255;

// The whole response is read before anything is written, so a value is printed
// complete or not at all. It lives in bss: it costs the binary nothing.
var response: [(1 << 20) + 4096]u8 = undefined;

pub fn main() u8 {
    var args = std.process.args();
    _ = args.skip();
    const label = args.next() orelse return usage();
    if (args.next() != null) return usage();

    if (!valid(label)) {
        say(2, "secret: a label is letters, digits, '.', '_' or '-'\n");
        return 2;
    }

    return fetch(label) catch |err| {
        say(2, "secret: the vault did not answer: ");
        say(2, @errorName(err));
        say(2, "\n");
        return 3;
    };
}

fn usage() u8 {
    say(2, "usage: secret <label>\n");
    return 2;
}

fn valid(label: []const u8) bool {
    if (label.len == 0 or label.len > max_label) return false;
    for (label) |c| {
        if (!std.ascii.isAlphanumeric(c) and c != '.' and c != '_' and c != '-') return false;
    }
    return true;
}

fn fetch(label: []const u8) !u8 {
    const fd = try posix.socket(posix.AF.UNIX, posix.SOCK.STREAM | posix.SOCK.CLOEXEC, 0);
    defer posix.close(fd);

    const addr = try std.net.Address.initUnix(socket_path);
    try posix.connect(fd, &addr.any, addr.getOsSockLen());

    var request: [max_label + 64]u8 = undefined;
    try writeAll(fd, try std.fmt.bufPrint(&request, "GET /v1/secret/{s} HTTP/1.0\r\nHost: vault\r\n\r\n", .{label}));

    var n: usize = 0;
    while (true) {
        if (n == response.len) return error.ResponseTooLarge;
        const got = try posix.read(fd, response[n..]);
        if (got == 0) break;
        n += got;
    }
    const raw = response[0..n];

    const end = std.mem.indexOf(u8, raw, "\r\n\r\n") orelse return error.MalformedResponse;
    if (end < 12 or !std.mem.startsWith(u8, raw, "HTTP/1.")) return error.MalformedResponse;
    const status = std.fmt.parseInt(u16, raw[9..12], 10) catch return error.MalformedResponse;
    const body = raw[end + 4 ..];

    if (status == 200) {
        try writeAll(1, body);
        return 0;
    }
    say(2, "secret: ");
    say(2, body);
    if (body.len == 0 or body[body.len - 1] != '\n') say(2, "\n");
    return 1;
}

fn writeAll(fd: posix.fd_t, bytes: []const u8) !void {
    var off: usize = 0;
    while (off < bytes.len) off += try posix.write(fd, bytes[off..]);
}

// say writes to a stream it cannot do anything about failing to write to.
fn say(fd: posix.fd_t, bytes: []const u8) void {
    writeAll(fd, bytes) catch {};
}
