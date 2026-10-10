import struct, sys

def parse_pe_resources(path):
    data = open(path, 'rb').read()
    # DOS header -> PE header
    if data[:2] != b'MZ':
        return None
    pe_off = struct.unpack_from('<I', data, 0x3C)[0]
    if data[pe_off:pe_off+4] != b'PE\0\0':
        return None
    # COFF header 20 bytes -> optional header
    coff = pe_off + 4
    opt = coff + 20
    magic = struct.unpack_from('<H', data, opt)[0]
    is_pe32p = magic == 0x20B  # PE32+
    # data directory starts after optional header standard fields
    if is_pe32p:
        num_rva = struct.unpack_from('<I', data, opt + 108)[0]
        rsrc_entry = opt + 112 + 2 * 8  # resource dir is index 2
    else:
        num_rva = struct.unpack_from('<I', data, opt + 92)[0]
        rsrc_entry = opt + 96 + 2 * 8
    if num_rva < 3:
        return None
    rsrc_rva, rsrc_size = struct.unpack_from('<II', data, rsrc_entry)
    if rsrc_rva == 0 or rsrc_size == 0:
        return []
    # section headers
    nsec = struct.unpack_from('<H', data, coff + 2)[0]
    opt_size = struct.unpack_from('<H', data, coff + 16)[0]
    sec_table = opt + opt_size
    sections = []
    for i in range(nsec):
        o = sec_table + i * 40
        name = data[o:o+8].rstrip(b'\0').decode('ascii', 'replace')
        vsize = struct.unpack_from('<I', data, o + 8)[0]
        vaddr = struct.unpack_from('<I', data, o + 12)[0]
        rawsize = struct.unpack_from('<I', data, o + 16)[0]
        rawptr = struct.unpack_from('<I', data, o + 20)[0]
        sections.append((name, vaddr, vsize, rawptr, rawsize))
    def rva_to_off(rva):
        for name, vaddr, vsize, rawptr, rawsize in sections:
            if vaddr <= rva < vaddr + max(vsize, rawsize):
                return rawptr + (rva - vaddr)
        return None
    out = []
    def walk(dir_off, root_rva, depth, name):
        n_entries = struct.unpack_from('<H', data, dir_off + 12)[0]
        n_ids = struct.unpack_from('<H', data, dir_off + 14)[0]
        for i in range(n_entries + n_ids):
            e = dir_off + 16 + i * 8
            eid = struct.unpack_from('<I', data, e)[0]
            child_rva = struct.unpack_from('<I', data, e + 4)[0]
            child_off = rva_to_off(child_rva)
            if child_rva & 0x80000000:
                child_off = rva_to_off(child_rva & 0x7FFFFFFF)
                walk(child_off, root_rva, depth + 1, name + [eid])
            else:
                # data entry
                d_rva, d_size, d_codepage, _ = struct.unpack_from('<IIII', data, child_off)
                doff = rva_to_off(d_rva)
                out.append({'path': name + [eid], 'size': d_size, 'off': doff})
    rsrc_off = rva_to_off(rsrc_rva)
    walk(rsrc_off, rsrc_rva, 0, [])
    return out

RT = {3: 'RT_ICON', 14: 'RT_GROUP_ICON', 16: 'RT_VERSION', 24: 'RT_MANIFEST'}
for label, path in [('ORIGINAL(artifacts)', r'C:\Users\Xcl\AppData\Local\Temp\AI-Radar-orig.exe'),
                    ('NEW(artifacts)', r'D:\Code\AI-Project\artifacts\AI-Radar.exe')]:
    print('=====', label)
    try:
        res = parse_pe_resources(path)
        if res is None:
            print('  not a PE')
        elif not res:
            print('  no resources')
        else:
            for r in res:
                typ = RT.get(r['path'][0], str(r['path'][0]))
                print(' ', r['path'], typ, 'size', r['size'])
    except Exception as e:
        print('  error', e)
