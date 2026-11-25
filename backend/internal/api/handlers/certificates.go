package handlers

import (
	"context"
	"net/http"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

const maxCertificateBodyBytes int64 = 4 * 1024 * 1024

type uploadCertificateRequest struct {
	Purpose string `json:"purpose"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

func (s *Server) handleListImportedCerts(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Certificates.ListCertificates(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if domains, routers, ok := s.certificateConsumers(r.Context()); ok {
		for i := range items {
			items[i].Config.UsedBy = certificates.UploadedUsage(items[i].Name, domains, routers)
		}
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleImportCert(w http.ResponseWriter, r *http.Request) {
	var in uploadCertificateRequest
	if !request.DecodeObject(w, r, &in, request.Options{MaxBytes: maxCertificateBodyBytes}) {
		return
	}
	res, err := s.deps.Certificates.ImportCertificate(r.Context(), r.PathValue("name"), in.Purpose, in.CertPEM, in.KeyPEM, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteImportedCert(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Certificates.DeleteCertificate(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleListACMECertificates(w http.ResponseWriter, r *http.Request) {
	raw, err := s.deps.ReadStaticConfig()
	if err != nil {
		response.WriteError(w, err)
		return
	}
	inventory := s.deps.ACMEObserver.Observe(raw)
	domains, routers, known := s.certificateConsumers(r.Context())
	annotateACMEInventory(&inventory, domains, routers, known)
	response.WriteJSON(w, http.StatusOK, inventory)
}

// annotateACMEInventory adds what uses each certificate and whether each resolver is in use.
// When the consumers are unknown, certificates carry no usage and every resolver counts as in
// use, so no storage warning is hidden.
func annotateACMEInventory(inventory *status.ACMEInventory, domains []models.UIResource[models.DomainConfig], routers []models.UIResource[models.RouterConfig], known bool) {
	used := certificates.ResolversInUse(domains, routers)
	for i := range inventory.Observations {
		inventory.Observations[i].InUse = !known || used[inventory.Observations[i].Resolver]
	}
	if !known {
		return
	}
	for i := range inventory.Certificates {
		c := &inventory.Certificates[i]
		c.UsedBy = certificates.ACMEUsage(append([]string{c.MainDomain}, c.SANs...), c.ResolverName, domains, routers)
	}
}

// certificateConsumers reads the domains and routers that can reference certificates. Usage is
// advisory: when either list is unavailable, ok is false and the certificates are returned without it.
func (s *Server) certificateConsumers(ctx context.Context) ([]models.UIResource[models.DomainConfig], []models.UIResource[models.RouterConfig], bool) {
	domains, err := s.deps.Domains.ListDomains(ctx)
	if err != nil {
		return nil, nil, false
	}
	routers, err := s.deps.Routers.ListRouters(ctx)
	if err != nil {
		return nil, nil, false
	}
	return domains, routers, true
}
