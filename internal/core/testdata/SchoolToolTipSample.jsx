import React from 'react';
import PropTypes from 'prop-types';
import CarIcon from 'assets/icons/car_icon.svg';
import FolderIcon from 'assets/icons/folder_icon.svg';
import './styles/schoolTooltip.scss';

export default function SchoolTooltip({
  name,
  city,
  province,
  hours,
  mins,
  kilometers,
  averageTuition,
  localized = {}
}) {
  return (
    <div className='school-pin-tooltip'>
      <div className='school-pin-tooltip__content'>
        <div className='school-pin-tooltip__content__school-name'>
          {name}
        </div>
        <div className='school-pin-tooltip__content__location'>
          {`${city && province ? `${city}, ${province}` : ''}`}
        </div>
        <div className='school-pin-tooltip__content__tuition'>
          <span className='school-pin-tooltip__content__tuition__text'>
            {''}
          </span>
          <span className='school-pin-tooltip__content__tuition__total'>
            {averageTuition}
          </span>
          <span className='school-pin-tooltip__content__tuition__yr'>
            {''}
          </span>
        </div>
        <div className='school-pin-tooltip__content__distance-programs'>
          <img
            className='school-pin-tooltip__content__distance-programs__car-icon'
            src={CarIcon}
            alt={localized['img.alt.carIcon'] || ''}
          />
          <div className='school-pin-tooltip__content__distance-programs__details'>
            {hours >= 1 && <span>{hours}{'hr'}{mins > 0 && ' '}</span>}
            {mins > 0 && <span>{mins}{' min'}</span>}{', '}
            {kilometers}{` ${localized['institutionsGroup.kilometers']}`}
          </div>
          <div className='school-pin-tooltip__content__distance-programs__program'>
            <img
              className='school-pin-tooltip__content__distance-programs__program__program-icon'
              src={FolderIcon}
              alt={localized['img.alt.folderIcon'] || ''}
            />
            <div className='school-pin-tooltip__content__distance-programs__program__details'>
              {0} {}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

SchoolTooltip.propTypes = {
  name: PropTypes.string,
  city: PropTypes.string,
  province: PropTypes.string,
  hours: PropTypes.number,
  mins: PropTypes.number,
  kilometers: PropTypes.string,
  averageTuition: PropTypes.string,
  programCount: PropTypes.number,
  localized: PropTypes.object
};
