import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Map as MapIcon } from 'lucide-react'
import { apiGet } from '../api'
import type { MapInfo } from '../types'
import { EmptyState, Spinner } from '../ui'

export default function MapsLanding() {
  const [maps, setMaps] = useState<MapInfo[] | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    apiGet<MapInfo[]>('/maps')
      .then(data => {
        setMaps(data)
        if (data.length) navigate(`/maps/${data[0].id}`, { replace: true })
      })
      .catch(() => setMaps([]))
  }, [navigate])

  if (maps === null) return <div className="page-center"><Spinner label="Cargando mapas" /></div>
  return (
    <div className="page-center">
      <EmptyState icon={<MapIcon size={26} />} title="No hay mapas configurados" message="Crea tu primer mapa desde el botón + del panel izquierdo." />
    </div>
  )
}
