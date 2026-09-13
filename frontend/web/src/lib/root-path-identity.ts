// rootPathIdentityKey mirrors the backend's pathnorm.RootPathKey: cleaned
// forward-slash form, with letter case folded only for syntactically
// Windows-style paths. The backend judges root changes by this key, so the
// frontend must too — otherwise a spelling/case-equivalent edit would clear
// folders the backend deliberately retained.
export function rootPathIdentityKey(path: string): string {
  let p = path.replace(/\\/g, '/')
  const isWindowsPath = /^[a-zA-Z]:\//.test(p) || /^\/\/\?/.test(p) || p.startsWith('//')
  if (isWindowsPath && p.length >= 3 && p[1] === ':') p = p.toLowerCase()
  return p
}