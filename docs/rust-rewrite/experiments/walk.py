import os,time
H=os.path.expanduser("~")
roots=[f"{H}/.claude",f"{H}/.codex",f"{H}/.pi/agent"]
for rep in range(3):
    t=time.perf_counter(); n=0; d={}
    stack=list(roots)
    while stack:
        p=stack.pop()
        try:
            with os.scandir(p) as it:
                for e in it:
                    if e.is_dir(follow_symlinks=False): stack.append(e.path)
                    elif e.is_file(follow_symlinks=False):
                        st=e.stat(follow_symlinks=False); d[e.path]=(st.st_size,st.st_mtime_ns); n+=1
        except OSError: pass
    t1=time.perf_counter()
    # lookup cost: hash map of n entries, 30k lookups
    keys=list(d)[:30000]; t2=time.perf_counter()
    for k in keys: d[k]
    t3=time.perf_counter()
    print(f"walk+stat {n} files {1000*(t1-t):.0f} ms; 30k dict lookups {1e6*(t3-t2):.0f} us")
