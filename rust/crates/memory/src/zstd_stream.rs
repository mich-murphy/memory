//! Bounded streaming decode of a zstd file: one or more whole frames, no trailing bytes, no
//! truncated frame, window at most 2^23 (Codex level 3 needs 2^21). Only a small input chunk
//! and one output chunk are ever held; the decoded file is never held whole.

use std::fs::File;
use std::io::{ErrorKind, Read};
use zstd::stream::raw::{DParameter, Decoder, InBuffer, Operation, OutBuffer};

/// Largest window the decoder accepts, as a power of two.
pub const WINDOW_LOG_MAX: u32 = 23;
const IN_CHUNK: usize = 64 * 1024;
const OUT_CHUNK: usize = 128 * 1024;

/// Why a decode stopped.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Failure {
    /// The bytes are not a valid stream of whole zstd frames within the window bound.
    Undecodable,
    /// The operating system failed while reading.
    Io(ErrorKind),
}

/// A decoder positioned in the decoded bytes of one file.
pub struct Stream {
    file: File,
    decoder: Decoder<'static>,
    inbuf: Vec<u8>,
    in_pos: usize,
    in_len: usize,
    eof: bool,
    /// The last frame ended (a frame has completed and none is open).
    finished: bool,
    done: bool,
    out: Vec<u8>,
    out_start: usize,
    /// Decoded offset of the next byte to deliver.
    pos: u64,
}

impl std::fmt::Debug for Stream {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Stream")
            .field("pos", &self.pos)
            .finish_non_exhaustive()
    }
}

impl Stream {
    /// Start decoding `file` from its beginning (the caller positions nothing).
    pub fn new(file: File) -> Result<Self, Failure> {
        let mut decoder = Decoder::new().map_err(|e| Failure::Io(e.kind()))?;
        decoder
            .set_parameter(DParameter::WindowLogMax(WINDOW_LOG_MAX))
            .map_err(|e| Failure::Io(e.kind()))?;
        Ok(Self {
            file,
            decoder,
            inbuf: vec![0; IN_CHUNK],
            in_pos: 0,
            in_len: 0,
            eof: false,
            finished: false,
            done: false,
            out: Vec::with_capacity(OUT_CHUNK),
            out_start: 0,
            pos: 0,
        })
    }

    /// Decoded offset of the next byte to deliver.
    pub const fn pos(&self) -> u64 {
        self.pos
    }

    /// Decode one more chunk into `out`; `false` at a valid end of stream.
    fn fill(&mut self) -> Result<bool, Failure> {
        self.out.clear();
        self.out_start = 0;
        loop {
            if self.done {
                return Ok(false);
            }
            if self.in_pos >= self.in_len {
                if self.eof {
                    if !self.finished {
                        return Err(Failure::Undecodable);
                    }
                    self.done = true;
                    return Ok(false);
                }
                let n = loop {
                    match self.file.read(&mut self.inbuf) {
                        Ok(n) => break n,
                        Err(e) if e.kind() == ErrorKind::Interrupted => {}
                        Err(e) => return Err(Failure::Io(e.kind())),
                    }
                };
                if n == 0 {
                    self.eof = true;
                } else {
                    self.in_len = n;
                    self.in_pos = 0;
                }
                continue;
            }
            if self.finished {
                self.decoder.reinit().map_err(|_| Failure::Undecodable)?;
                self.finished = false;
            }
            let src = self
                .inbuf
                .get(self.in_pos..self.in_len)
                .ok_or(Failure::Undecodable)?;
            let mut input = InBuffer::around(src);
            let hint = {
                let mut output = OutBuffer::around(&mut self.out);
                self.decoder
                    .run(&mut input, &mut output)
                    .map_err(|_| Failure::Undecodable)?
            };
            let consumed = input.pos();
            self.in_pos += consumed;
            self.finished = hint == 0;
            if !self.out.is_empty() {
                return Ok(true);
            }
            if consumed == 0 && hint != 0 {
                return Err(Failure::Undecodable);
            }
        }
    }

    /// Total decoded length of the rest of the stream; validates it to the end.
    pub fn drain_len(&mut self) -> Result<u64, Failure> {
        let mut total = self.pos;
        // Whatever is buffered counts too.
        total += (self.out.len() - self.out_start) as u64;
        while self.fill()? {
            total += self.out.len() as u64;
        }
        self.out.clear();
        self.out_start = 0;
        Ok(total)
    }

    /// Decoded bytes `[start, start + len)`, clipped at the end of the stream. `start` must
    /// not be before [`Stream::pos`]. `cap` is only the buffer's initial capacity.
    pub fn take(&mut self, start: u64, len: u64, cap: usize) -> Result<Vec<u8>, Failure> {
        while self.pos < start {
            if self.out_start >= self.out.len() && !self.fill()? {
                return Ok(Vec::new());
            }
            let avail = (self.out.len() - self.out_start) as u64;
            let n = avail.min(start - self.pos);
            self.out_start += usize::try_from(n).map_err(|_| Failure::Undecodable)?;
            self.pos += n;
        }
        let mut buf = Vec::with_capacity(cap);
        while (buf.len() as u64) < len {
            if self.out_start >= self.out.len() && !self.fill()? {
                break;
            }
            let avail = self.out.get(self.out_start..).unwrap_or_default();
            let want = usize::try_from(len - buf.len() as u64).unwrap_or(usize::MAX);
            let n = avail.len().min(want);
            buf.extend_from_slice(avail.get(..n).unwrap_or_default());
            self.out_start += n;
            self.pos += n as u64;
        }
        Ok(buf)
    }
}
