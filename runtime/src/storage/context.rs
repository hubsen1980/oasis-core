//! Thread-local storage context.
//!
//! The storage context is a convenient way to share CAS and MKVS
//! implementations across the current thread.
use std::{cell::RefCell, sync::Arc};

use super::{KeyValue, MKVS};

struct Ctx {
    mkvs: *mut dyn MKVS,
    untrusted_local: Arc<dyn KeyValue>,
}

thread_local! {
    static CTX: RefCell<Option<Ctx>> = RefCell::new(None);
}

struct CtxGuard;

impl CtxGuard {
    fn new<M>(mkvs: &mut M, untrusted_local: Arc<dyn KeyValue>) -> Self
    where
        M: MKVS,
    {
        // This is safe because the reference is only valid within StorageContext::enter within
        // the same thread.
        let mkvs = unsafe { std::mem::transmute::<&mut dyn MKVS, &mut (dyn MKVS + 'static)>(mkvs) };

        CTX.with(|ctx| {
            assert!(ctx.borrow().is_none(), "nested enter is not allowed");
            ctx.borrow_mut().replace(Ctx {
                mkvs,
                untrusted_local,
            });
        });

        CtxGuard
    }
}

impl Drop for CtxGuard {
    fn drop(&mut self) {
        CTX.with(|local| {
            drop(local.borrow_mut().take());
        });
    }
}

/// Thread-local storage context.
pub struct StorageContext;

impl StorageContext {
    /// Enter the storage context.
    pub fn enter<M, F, R>(mkvs: &mut M, untrusted_local: Arc<dyn KeyValue>, f: F) -> R
    where
        M: MKVS,
        F: FnOnce() -> R,
    {
        let _guard = CtxGuard::new(mkvs, untrusted_local);
        f()
    }

    /// Run a closure with the thread-local storage context.
    ///
    /// # Panics
    ///
    /// Will panic if called outside `StorageContext::enter`.
    pub fn with_current<F, R>(f: F) -> R
    where
        F: FnOnce(&mut dyn MKVS, &Arc<dyn KeyValue>) -> R,
    {
        CTX.with(|ctx| {
            let ctx = ctx.borrow();
            let ctx_ref = ctx.as_ref().expect("must only be called while entered");
            // This is safe because the reference is only valid within StorageContext::enter within
            // the same thread.
            let mkvs_ref = unsafe { ctx_ref.mkvs.as_mut().expect("pointer is never null") };

            f(mkvs_ref, &ctx_ref.untrusted_local)
        })
    }
}
