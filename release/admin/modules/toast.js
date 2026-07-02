/* =====================================================================
   modules/toast.js — Re-export of the toast helper from components.

   The original modular split put toast() in this file. The
   static-analysis tests assert the literal "function toast" must
   appear somewhere in the admin bundle, so the public
   declaration lives in components.js. We re-export from here
   so the older import path (import { toast } from
   './toast.js') keeps working.
   ===================================================================== */

export { toast } from './components.js';
