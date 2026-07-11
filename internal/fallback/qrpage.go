package fallback

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"

	"trustpanel/internal/paths"
)

// This file serves a self-contained, client-side deep-link → QR / connect page at
// an unguessable path on the camouflage origin (the public apex). The panel hands
// out a link to it (`https://<apex><path>#tt=<payload>`); opening that link shows
// a polished landing — QR code, an "open in app" deep link, and the app-store
// links — entirely in the browser, WITHOUT the payload ever touching a third
// party (e.g. trusttunnel.org):
//
//   - One static HTML document with the QR library inlined; no runtime requests.
//   - The deep-link payload is read from the URL *fragment* (`#tt=…`) or typed
//     into the form, and processed entirely in the browser. Fragments are never
//     sent to the server and the form never POSTs, so the username/password
//     embedded in the deep link never leave the device.
//   - The path is unguessable, omitted from robots.txt/sitemap.xml, and the
//     response is marked noindex.
//
// Two-faced by design (operator request): with NO valid payload the page is a
// bland, unbranded "tlv-probe" diagnostic — anyone who stumbles onto the path
// sees something that looks internal/service-y and gives nothing away. Only when
// a valid payload is present (fragment or pasted) does it pull in the landing
// styles and reveal the QR / connect UI for the intended user.
//
// The path defaults to a high-entropy constant (paths.DefaultQRPath, shared with
// the panel so it can build matching landing links) and can be overridden per
// deployment via FALLBACK_QR_PATH. The app-store links are overridable via
// FALLBACK_APP_IOS / FALLBACK_APP_ANDROID.

// qrLibJS is the canonical qrcode-generator (Kazuhiko Arase, MIT), inlined so
// the page is fully self-contained and never fetches anything at runtime.
//
//go:embed assets/qrcode.min.js
var qrLibJS string

// qrPagePath returns the route the QR page is served at (env-overridable).
func qrPagePath() string {
	p := strings.TrimSpace(envOr("FALLBACK_QR_PATH", paths.DefaultQRPath))
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// appLinks returns the iOS/Android store links shown on the landing.
func appLinks() (ios, android string) {
	return envOr("FALLBACK_APP_IOS", "https://agrd.io/ios_trusttunnel"),
		envOr("FALLBACK_APP_ANDROID", "https://agrd.io/android_trusttunnel")
}

// handleQRPage serves the static, self-contained page. No query/body is read:
// everything happens client-side from the URL fragment or the form.
func handleQRPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte(qrPageHTML))
}

// qrPageHTML is assembled once at init. The scripts come AFTER the form
// markup so the DOM nodes (#in/#go/#out) exist when the app script runs.
var qrPageHTML = buildQRPage()

func buildQRPage() string {
	ios, android := appLinks()
	cfgJS := "var APP_IOS=" + strconv.Quote(ios) +
		", APP_ANDROID=" + strconv.Quote(android) +
		", LANDING_CSS=" + strconv.Quote(landingCSS) + ";\n"
	return qrPageHead +
		"<script>\n" + qrLibJS + "\n</script>\n" +
		"<script>\n" + cfgJS + qrAppJS + "\n</script>\n" +
		qrPageTail
}

// qrPageHead is the bland cover: a small, unbranded "tlv-probe" diagnostic. Its
// inline styles are deliberately utilitarian; the rich landing styles live in
// landingCSS and are injected by JS only once a valid payload is decoded.
const qrPageHead = `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow, noarchive">
<title>tlv-probe</title>
<style>
*{box-sizing:border-box}
body{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;margin:0;padding:16px;color:#222;background:#fff;font-size:13px;line-height:1.5}
#app{max-width:560px}
#cover h1{font-size:13px;font-weight:600;margin:0 0 2px}
#cover .meta{color:#777;margin:0 0 12px}
#in{width:100%;font-family:inherit;font-size:12px;padding:6px;border:1px solid #ccc;border-radius:4px;background:#fafafa}
.row{display:flex;gap:8px;flex-wrap:wrap;margin-top:8px}
#go{font:inherit;padding:5px 12px;border:1px solid #bbb;border-radius:4px;background:#f2f2f2;color:#222;cursor:pointer}
#go:hover{background:#e8e8e8}
#out{margin-top:14px}
.e{color:#a00}
</style>
</head><body>
<div id="app">
<div id="cover">
<h1>tlv-probe</h1>
<p class="meta">tlv decoder · status: ok</p>
<textarea id="in" rows="3" placeholder="payload" spellcheck="false" autocapitalize="off" autocomplete="off"></textarea>
<div class="row"><button id="go" type="button">decode</button></div>
</div>
<div id="out"></div>
</div>
`

const qrPageTail = `</body></html>`

// landingCSS styles the revealed landing (body gets class "landing"). Kept out of
// the cover's static markup and injected at render time so a stray visitor's view
// is just the plain diagnostic. Plain ASCII so it survives strconv.Quote into JS.
const landingCSS = `
body.landing{font-family:Inter,ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#f5f7fb;color:#13233b;display:flex;justify-content:center;padding:32px 16px}
body.landing #cover{display:none}
body.landing #app{max-width:400px;width:100%}
body.landing #out{margin:0}
.card{background:#fff;border:1px solid #e1e8f0;border-radius:16px;box-shadow:0 18px 50px rgba(19,35,59,.12);padding:28px 24px;text-align:center}
.card h1{font-size:19px;font-weight:800;margin:0 0 4px}
.card .sub{color:#64748b;font-size:13px;margin:0 0 20px}
.qr{display:flex;justify-content:center;margin:0 0 20px}
.qr svg{width:230px;height:230px;display:block;background:#fff;border:1px solid #eef2f7;border-radius:12px;padding:10px}
.btn{display:flex;align-items:center;justify-content:center;width:100%;min-height:46px;margin-top:10px;padding:0 16px;border-radius:10px;border:1px solid #d6dee8;background:#fff;color:#13233b;font:inherit;font-weight:700;text-decoration:none;cursor:pointer}
.btn.primary{background:#246bfe;border-color:#246bfe;color:#fff;box-shadow:0 10px 24px rgba(36,107,254,.25)}
.btn.primary:hover{background:#1b5be0}
.stores{display:flex;gap:10px;margin-top:16px}
.stores .btn{margin-top:0;font-size:13px}
.details{margin-top:20px;text-align:left;border-top:1px solid #eef2f7;padding-top:6px}
.details summary{cursor:pointer;font-weight:700;font-size:13px;color:#13233b;list-style:none;padding:8px 0;user-select:none}
.details summary::-webkit-details-marker{display:none}
.details summary:before{content:"\25b8\00a0";color:#94a3b8}
.details[open] summary:before{content:"\25be\00a0"}
.field{display:grid;gap:4px;margin-top:10px}
.field label{font-size:12px;font-weight:600;color:#64748b}
.field input,.field textarea,.field select{font:inherit;font-size:13px;padding:8px 10px;border:1px solid #d6dee8;border-radius:8px;background:#fff;color:#13233b;width:100%}
.field input:focus,.field textarea:focus,.field select:focus{outline:3px solid rgba(36,107,254,.15);border-color:#246bfe}
.field textarea{font-family:ui-monospace,Menlo,monospace;font-size:12px;resize:vertical}
.field .hint{font-size:11px;color:#94a3b8}
.check{display:flex;align-items:flex-start;gap:9px;margin-top:14px;font-size:13px;color:#13233b;line-height:1.35}
.check input{width:18px;height:18px;margin:1px 0 0;flex:none}
.check .hint{display:block;font-size:11px;color:#94a3b8;font-weight:400}
.cfgerr{color:#a00;font-size:12px;margin-top:8px;min-height:14px}
.dl{width:100%;margin-top:14px;font-family:ui-monospace,Menlo,monospace;font-size:11px;color:#64748b;border:1px solid #eef2f7;border-radius:8px;padding:8px;background:#fafbfd;resize:none}
.e{color:#a00}
`

// qrAppJS: no template literals / backticks (so it can live in a Go raw string).
// APP_IOS / APP_ANDROID / LANDING_CSS are injected ahead of this script.
const qrAppJS = `
(function(){
  var IN=document.getElementById('in'), OUT=document.getElementById('out');
  var styled=false, CUR='', ORIG_CERT=null;

  function fail(){ OUT.innerHTML='<span class="e">invalid input</span>'; }

  function esc(s){ return String(s).replace(/[&<>"']/g,function(c){
    return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]; }); }

  function gv(id){ return (document.getElementById(id).value||'').trim(); }
  function setv(id,v){ document.getElementById(id).value=(v==null?'':v); }
  // Split a textarea into a clean list (newline- or comma-separated).
  function lines(s){ return String(s||'').split(/[\n,]+/).map(function(x){return x.trim();}).filter(Boolean); }

  // Pull in the landing styles only on a valid payload (operator request: the
  // bare page must look like a service/test endpoint, not a product).
  function dress(){
    if(!styled){
      var s=document.createElement('style'); s.textContent=LANDING_CSS;
      document.head.appendChild(s); styled=true;
    }
    document.body.className='landing';
  }
  function undress(){ document.body.className=''; }

  // base64url -> Uint8Array (tolerant of padding/whitespace).
  function b64(s){
    s=s.replace(/-/g,'+').replace(/_/g,'/').replace(/\s+/g,'');
    while(s.length%4) s+='=';
    var bin=atob(s), out=new Uint8Array(bin.length);
    for(var i=0;i<bin.length;i++) out[i]=bin.charCodeAt(i);
    return out;
  }
  // Uint8Array/byte-array -> base64url (no padding).
  function b64url(arr){
    var s=''; for(var i=0;i<arr.length;i++) s+=String.fromCharCode(arr[i]&0xff);
    return btoa(s).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
  }

  // QUIC/TLS variable-length integer (RFC 9000 16). Returns [value,nextIndex].
  function varint(b,i){
    if(i>=b.length) throw 0;
    var f=b[i], p=f>>6;
    if(p===0) return [f&0x3f, i+1];
    if(p===1){ if(i+1>=b.length) throw 0; return [((f&0x3f)<<8)|b[i+1], i+2]; }
    if(p===2){ if(i+3>=b.length) throw 0; return [(f&0x3f)*0x1000000+(b[i+1]<<16)+(b[i+2]<<8)+b[i+3], i+4]; }
    if(i+7>=b.length) throw 0;
    var v=f&0x3f; for(var k=1;k<8;k++) v=v*256+b[i+k]; return [v,i+8];
  }
  // Append v to out using the same varint encoding (lengths/tags only -> <2^30).
  function putVarint(out,v){
    if(v<0x40) out.push(v);
    else if(v<0x4000) out.push(0x40|(v>>8), v&0xff);
    else out.push(0x80|((v>>24)&0xff),(v>>16)&0xff,(v>>8)&0xff,v&0xff);
  }
  function enc(s){ return new TextEncoder().encode(s); } // string -> UTF-8 bytes
  function tlv(out,tag,bytes){ putVarint(out,tag); putVarint(out,bytes.length);
    for(var i=0;i<bytes.length;i++) out.push(bytes[i]); }

  function dec(bytes){ // UTF-8 -> string
    try{ return new TextDecoder().decode(bytes); }
    catch(e){ return String.fromCharCode.apply(null,bytes); }
  }

  // Parse the TLV payload, requiring the fields a usable endpoint must carry.
  // Defaults match the deep-link spec (has_ipv6=true, others off/empty).
  function parse(bytes){
    var i=0, o={host:null,user:null,pass:null,name:null,addrs:[],dns:[],
      sni:'',ipv6:true,skip:false,antidpi:false,proto:1,crand:'',cert:null};
    while(i<bytes.length){
      var t=varint(bytes,i); var tag=t[0]; i=t[1];
      var l=varint(bytes,i); var len=l[0]; i=l[1];
      if(i+len>bytes.length) throw 0;
      var val=bytes.subarray(i,i+len); i+=len;
      if(tag===1) o.host=dec(val);
      else if(tag===2) o.addrs.push(dec(val));
      else if(tag===3) o.sni=dec(val);
      else if(tag===4) o.ipv6=(val.length>0 && val[0]!==0);
      else if(tag===5) o.user=dec(val);
      else if(tag===6) o.pass=dec(val);
      else if(tag===7) o.skip=(val.length>0 && val[0]!==0);
      else if(tag===8) o.cert=val.slice();           // raw DER chain
      else if(tag===9) o.proto=(val.length>0?varint(val,0)[0]:1);
      else if(tag===10) o.antidpi=(val.length>0 && val[0]!==0);
      else if(tag===11) o.crand=dec(val);
      else if(tag===12) o.name=dec(val);
      else if(tag===13){ var di=0; while(di<val.length){
        var d=varint(val,di); var dl=d[0]; di=d[1];
        o.dns.push(dec(val.subarray(di,di+dl))); di+=dl; } }
    }
    if(!o.host||!o.user||!o.pass||o.addrs.length===0) throw 0;
    return o;
  }

  // Extract concatenated DER bytes from a PEM bundle (one or more certificates).
  // Returns an array of bytes, or null if the text is blank; throws on garbage.
  function pemToDer(text){
    text=String(text||'').trim();
    if(!text) return null;
    var re=/-----BEGIN CERTIFICATE-----([A-Za-z0-9+\/=\s]+?)-----END CERTIFICATE-----/g;
    var out=[], m, found=false;
    while((m=re.exec(text))){
      found=true;
      var bin=atob(m[1].replace(/\s+/g,''));
      for(var i=0;i<bin.length;i++) out.push(bin.charCodeAt(i));
    }
    if(!found) throw 0;
    return out;
  }

  // Encode edited endpoint fields back into the TLV wire format (inverse of parse),
  // mirroring the server-side field order in clientcfg.buildDeepLinkPayload.
  // Fields at their spec default are omitted, so an unedited link re-encodes to
  // the exact same bytes.
  function encode(cfg){
    var out=[];
    tlv(out,0,[1]);                 // version = 1
    tlv(out,1,enc(cfg.host));       // hostname (cert name)
    if(cfg.name) tlv(out,12,enc(cfg.name));
    if(cfg.sni) tlv(out,3,enc(cfg.sni));
    if(cfg.crand) tlv(out,11,enc(cfg.crand));
    tlv(out,5,enc(cfg.user));
    tlv(out,6,enc(cfg.pass));
    for(var i=0;i<cfg.addrs.length;i++) tlv(out,2,enc(cfg.addrs[i]));
    if(cfg.ipv6===false) tlv(out,4,[0]);       // default true -> omit when true
    if(cfg.skip) tlv(out,7,[1]);               // default false
    if(cfg.antidpi) tlv(out,10,[1]);           // default false
    if(cfg.proto===2) tlv(out,9,[2]);          // default http2(1) -> omit
    if(cfg.cert&&cfg.cert.length) tlv(out,8,cfg.cert);
    if(cfg.dns&&cfg.dns.length){
      var arr=[]; for(var j=0;j<cfg.dns.length;j++){ var db=enc(cfg.dns[j]);
        putVarint(arr,db.length); for(var k=0;k<db.length;k++) arr.push(db[k]); }
      tlv(out,13,arr);
    }
    return b64url(out);
  }

  // Extract the base64url payload from a tt:// URI, a #tt= URL, or raw base64.
  function payload(raw){
    raw=(raw||'').trim();
    var m=raw.match(/[#?&]tt=([^&\s]+)/);
    if(m) return m[1];
    if(raw.indexOf('tt://?')===0) return raw.slice(6);
    if(raw.indexOf('tt://')===0) return raw.slice(5);
    return raw;
  }

  // (Re)generate the QR + deep link + copy field from the given endpoint fields.
  function paint(cfg){
    CUR='tt://?'+encode(cfg);
    var q=qrcode(0,'M'); q.addData(CUR); q.make();
    document.getElementById('qr').innerHTML=q.createSvgTag(6,0);
    document.getElementById('open').setAttribute('href',CUR);
    setv('dl',CUR);
  }

  // Read the editable Configuration-details form into an endpoint object. The
  // certificate is kept as-is unless a new PEM is pasted (pemToDer may throw).
  function readForm(){
    var cert=ORIG_CERT, pem=gv('f_cert');
    if(pem) cert=pemToDer(pem);
    return {
      name:gv('f_name'), host:gv('f_host'), sni:gv('f_sni'),
      user:gv('f_user'), pass:gv('f_pass'),
      addrs:lines(gv('f_addrs')), dns:lines(gv('f_dns')),
      crand:gv('f_crand'),
      proto:(document.getElementById('f_proto').value==='2'?2:1),
      ipv6:document.getElementById('f_ipv6').checked,
      skip:document.getElementById('f_skip').checked,
      antidpi:document.getElementById('f_antidpi').checked,
      cert:cert
    };
  }

  // Render the landing for a valid payload: QR + open-in-app + store links +
  // an editable "Configuration details" block (the full client field set)
  // that regenerates the QR/link.
  function render(p){
    var cfg=parse(b64(p));
    ORIG_CERT=cfg.cert;
    dress();
    OUT.innerHTML =
      '<div class="card">' +
        '<h1>Connect</h1>' +
        '<p class="sub">Scan the QR code, or open the link on your device.</p>' +
        '<div class="qr" id="qr"></div>' +
        '<a class="btn primary" id="open" href="#">Open in app</a>' +
        '<button class="btn" id="copy" type="button">Copy link</button>' +
        '<div class="stores">' +
          '<a class="btn" target="_blank" rel="noopener" href="'+esc(APP_IOS)+'">App Store (iOS)</a>' +
          '<a class="btn" target="_blank" rel="noopener" href="'+esc(APP_ANDROID)+'">Google Play (Android)</a>' +
        '</div>' +
        '<details class="details">' +
          '<summary>Configuration details</summary>' +
          '<div class="field"><label for="f_name">Server name</label>' +
            '<input id="f_name" type="text" autocomplete="off" placeholder="optional"></div>' +
          '<div class="field"><label for="f_host">Domain name from server certificate</label>' +
            '<input id="f_host" type="text" autocomplete="off"></div>' +
          '<div class="field"><label for="f_sni">Custom SNI</label>' +
            '<input id="f_sni" type="text" autocomplete="off" placeholder="optional">' +
            '<span class="hint">overrides the name sent in the TLS handshake</span></div>' +
          '<div class="field"><label for="f_addrs">Addresses</label>' +
            '<textarea id="f_addrs" rows="2" spellcheck="false"></textarea>' +
            '<span class="hint">one host:port per line</span></div>' +
          '<div class="field"><label for="f_user">Username</label>' +
            '<input id="f_user" type="text" autocomplete="off"></div>' +
          '<div class="field"><label for="f_pass">Password</label>' +
            '<input id="f_pass" type="text" autocomplete="off"></div>' +
          '<div class="field"><label for="f_proto">Upstream protocol</label>' +
            '<select id="f_proto"><option value="1">HTTP/2</option>' +
            '<option value="2">HTTP/3</option></select></div>' +
          '<div class="field"><label for="f_dns">DNS upstreams</label>' +
            '<textarea id="f_dns" rows="2" spellcheck="false"></textarea>' +
            '<span class="hint">one per line; optional</span></div>' +
          '<div class="field"><label for="f_crand">Client Random</label>' +
            '<input id="f_crand" type="text" autocomplete="off" placeholder="optional">' +
            '<span class="hint">hex, format prefix[/mask]</span></div>' +
          '<div class="field"><label for="f_cert">Server certificate (PEM)</label>' +
            '<textarea id="f_cert" rows="3" spellcheck="false" placeholder="optional"></textarea>' +
            '<span class="hint" id="f_certnote">paste a PEM chain to pin a self-signed certificate</span></div>' +
          '<label class="check"><input id="f_ipv6" type="checkbox">' +
            '<span>Allow IPv6 connections via the server' +
            '<span class="hint">enable only if the server has working IPv6</span></span></label>' +
          '<label class="check"><input id="f_skip" type="checkbox">' +
            '<span>Skip certificate verification' +
            '<span class="hint">insecure; only for testing</span></span></label>' +
          '<label class="check"><input id="f_antidpi" type="checkbox">' +
            '<span>Anti-DPI' +
            '<span class="hint">fragment the TLS handshake to evade DPI</span></span></label>' +
          '<div class="cfgerr" id="cfgerr"></div>' +
          '<button class="btn" id="regen" type="button">Update QR &amp; link</button>' +
          '<textarea class="dl" id="dl" rows="3" readonly></textarea>' +
        '</details>' +
      '</div>';

    // Prefill via .value/.checked (no HTML escaping pitfalls for user data).
    setv('f_name',cfg.name||''); setv('f_host',cfg.host); setv('f_sni',cfg.sni||'');
    setv('f_user',cfg.user); setv('f_pass',cfg.pass);
    setv('f_addrs',cfg.addrs.join('\n')); setv('f_dns',(cfg.dns||[]).join('\n'));
    setv('f_crand',cfg.crand||'');
    document.getElementById('f_proto').value=(cfg.proto===2?'2':'1');
    document.getElementById('f_ipv6').checked=cfg.ipv6;
    document.getElementById('f_skip').checked=cfg.skip;
    document.getElementById('f_antidpi').checked=cfg.antidpi;
    if(cfg.cert&&cfg.cert.length){
      document.getElementById('f_certnote').textContent=
        'certificate embedded ('+cfg.cert.length+' bytes); paste a new PEM to replace it';
    }
    paint(cfg);

    var cp=document.getElementById('copy');
    cp.addEventListener('click', function(){
      try{ navigator.clipboard.writeText(CUR); cp.textContent='Copied';
           setTimeout(function(){ cp.textContent='Copy link'; },1200); }
      catch(e){ var d=document.getElementById('dl'); d.focus(); d.select(); }
    });
    document.getElementById('regen').addEventListener('click', function(){
      var err=document.getElementById('cfgerr'), c;
      try{ c=readForm(); }
      catch(e){ err.textContent='Certificate must be valid PEM (BEGIN/END CERTIFICATE).'; return; }
      if(!c.host||!c.user||!c.pass||!c.addrs.length){
        err.textContent='Domain, username, password and at least one address are required.'; return; }
      err.textContent=''; paint(c);
    });
  }

  function run(){
    OUT.innerHTML='';
    var p=payload(IN.value);
    if(!p){ undress(); fail(); return; }
    try{ render(p); } catch(e){ undress(); fail(); }
  }

  document.getElementById('go').addEventListener('click', run);
  // Auto-run when the payload arrives in the fragment (never sent to the server).
  var h=location.hash.match(/tt=([^&]+)/);
  if(h){ IN.value=h[1]; run(); }
})();
`
